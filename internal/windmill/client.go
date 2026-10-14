package windmill

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

type Client struct {
	base     url.URL
	token    string
	maxBytes int64
	http     *http.Client
}

type APIError struct {
	Kind   string
	Status int
}

func (err *APIError) Error() string {
	if err.Status != 0 {
		return fmt.Sprintf("Windmill request failed: %s (HTTP %d)", err.Kind, err.Status)
	}
	return "Windmill request failed: " + err.Kind
}

func New(base *url.URL, token string, timeout time.Duration, maxBytes int64) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 3
	transport.ResponseHeaderTimeout = timeout
	return &Client{
		base: *base, token: token, maxBytes: maxBytes,
		http: &http.Client{Timeout: timeout, Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (client *Client) Close() { client.http.CloseIdleConnections() }

func (client *Client) Get(ctx context.Context, collector string, query url.Values) (any, int, error) {
	var result any
	var path string
	switch collector {
	case "health_status":
		path, result = "/health/status", &Health{}
	case "health_detailed":
		path, result = "/health/detailed", &Detailed{}
	case "queue_status":
		path, result = "/workers/queue_status", &[]Tag{}
	case "queue_counts":
		path, result = "/workers/queue_counts", &map[string]Required[int64]{}
	case "runnable_stats":
		path, result = "/workers/runnable_stats", &[]Runnable{}
	case "workers":
		path, result = "/workers/list", &[]Worker{}
	case "job_tags":
		path, result = "/jobs/completed/count_by_tag", &[]CompletedTag{}
	case "job_count", "workspace_queue":
		workspace := query.Get("workspace")
		if workspace == "" || workspace == "." || workspace == ".." || strings.ContainsAny(workspace, "/\\?#") {
			return nil, 0, &APIError{Kind: "invalid_request"}
		}
		parameters := url.Values{}
		for key, values := range query {
			if key != "workspace" {
				parameters[key] = values
			}
		}
		query = parameters
		if collector == "job_count" {
			path, result = "/w/"+workspace+"/jobs/completed/count_jobs", &Required[int64]{}
		} else {
			path, result = "/w/"+workspace+"/jobs/queue/count", &QueueInventory{}
		}
	default:
		return nil, 0, &APIError{Kind: "unknown_collector"}
	}
	target := client.base
	target.Path = strings.TrimRight(target.Path, "/") + "/api" + path
	target.RawPath = ""
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, 0, &APIError{Kind: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "windmill-exporter")
	if collector != "health_status" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, 0, &APIError{Kind: errorKind(err)}
	}
	defer func() { _ = response.Body.Close() }()
	status := response.StatusCode
	healthUnavailable := strings.HasPrefix(collector, "health_") && status == http.StatusServiceUnavailable
	if status != http.StatusOK && !healthUnavailable {
		return nil, status, &APIError{Kind: "http_error", Status: status}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.maxBytes+1))
	if err != nil {
		return nil, status, &APIError{Kind: errorKind(err), Status: status}
	}
	if int64(len(body)) > client.maxBytes {
		return nil, status, &APIError{Kind: "response_too_large", Status: status}
	}
	if err := json.Unmarshal(body, result); err != nil || !valid(reflect.ValueOf(result)) {
		return nil, status, &APIError{Kind: "invalid_response", Status: status}
	}
	if !uniqueRows(result) {
		return nil, status, &APIError{Kind: "invalid_response", Status: status}
	}
	if strings.HasPrefix(collector, "health_") {
		var state string
		switch health := result.(type) {
		case *Health:
			state = health.Status.Value
		case *Detailed:
			state = health.Status.Value
		}
		if (state != "healthy" && state != "degraded" && state != "unhealthy") || (healthUnavailable && state != "unhealthy") {
			return nil, status, &APIError{Kind: "invalid_response", Status: status}
		}
	}
	return result, status, nil
}

func valid(value reflect.Value) bool {
	if value.Type() == reflect.TypeFor[time.Time]() {
		return !value.Interface().(time.Time).IsZero()
	}
	switch value.Kind() {
	case reflect.Pointer:
		return value.IsNil() || valid(value.Elem())
	case reflect.Struct:
		if present := value.FieldByName("Present"); present.IsValid() && !present.Bool() {
			return false
		}
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() && !valid(value.Field(index)) {
				return false
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			return false
		}
		for index := 0; index < value.Len(); index++ {
			if !valid(value.Index(index)) {
				return false
			}
		}
	case reflect.Map:
		if value.IsNil() {
			return false
		}
		for _, key := range value.MapKeys() {
			if key.String() == "" || !valid(value.MapIndex(key)) {
				return false
			}
		}
	case reflect.Int, reflect.Int64:
		return value.Int() >= 0
	case reflect.Float64:
		return value.Float() >= 0
	case reflect.String:
		return value.String() != ""
	}
	return true
}

func uniqueRows(data any) bool {
	switch rows := data.(type) {
	case *[]CompletedTag:
		seen := map[string]bool{}
		for _, row := range *rows {
			if seen[row.Tag.Value] {
				return false
			}
			seen[row.Tag.Value] = true
		}
	case *[]Worker:
		seen := map[string]bool{}
		for _, row := range *rows {
			if seen[row.Name.Value] {
				return false
			}
			seen[row.Name.Value] = true
		}
	case *[]Tag:
		seen := map[string]bool{}
		for _, row := range *rows {
			if seen[row.Tag.Value] {
				return false
			}
			seen[row.Tag.Value] = true
		}
	case *[]Runnable:
		if len(*rows) > 1000 {
			return false
		}
		seen := map[[3]string]bool{}
		for _, row := range *rows {
			key := [3]string{row.Workspace.Value, row.Path.Value, row.WorkerGroup.Value}
			if seen[key] {
				return false
			}
			seen[key] = true
		}
	}
	return true
}

func errorKind(err error) string {
	var network net.Error
	var certificate x509.UnknownAuthorityError
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network) && network.Timeout():
		return "timeout"
	case errors.As(err, &certificate):
		return "server_certificate_invalid"
	default:
		return "transport_error"
	}
}
