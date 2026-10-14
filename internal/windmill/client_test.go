package windmill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name, collector, body string
		status                int
		wantError             bool
	}{
		{"healthy", "health_status", `{"status":"healthy","checked_at":"2026-10-14T00:00:00Z","database_healthy":true,"workers_alive":2,"future":1}`, 200, false},
		{"unhealthy", "health_status", `{"status":"unhealthy","checked_at":"2026-10-14T00:00:00Z","database_healthy":false,"workers_alive":0}`, 503, false},
		{"detailed omitted checks", "health_detailed", `{"status":"unhealthy","checked_at":"2026-10-14T00:00:00Z","version":"CE test","checks":{"database":{"healthy":false,"latency_ms":5000,"pool":{"size":2,"idle":0,"max_connections":10}},"readiness":{"healthy":false}}}`, 503, false},
		{"missing", "health_status", `{"status":"healthy","checked_at":"2026-10-14T00:00:00Z","database_healthy":true}`, 200, true},
		{"null", "health_status", `{"status":"healthy","checked_at":null,"database_healthy":true,"workers_alive":0}`, 200, true},
		{"bad timestamp", "health_status", `{"status":"healthy","checked_at":"bad","database_healthy":true,"workers_alive":0}`, 200, true},
		{"missing delay", "queue_status", `[{"tag":"python","waiting":0,"running":1,"workers":2}]`, 200, false},
		{"null delay", "queue_status", `[{"tag":"python","waiting":0,"delay":null,"running":1,"workers":2}]`, 200, false},
		{"negative", "queue_counts", `{"python":-1}`, 200, true},
		{"null count", "queue_counts", `{"python":null}`, 200, true},
		{"duplicate tag", "queue_status", `[{"tag":"python","waiting":0,"running":1,"workers":2},{"tag":"python","waiting":0,"running":1,"workers":2}]`, 200, true},
		{"empty", "queue_status", `[]`, 200, false},
		{"worker optional readings", "workers", `[{"worker":"worker-1","worker_instance":"pod-1","worker_group":"default","started_at":"2026-10-14T00:00:00Z","wm_version":"v1.833.0","jobs_executed":51,"draining":false,"memory":null}]`, 200, false},
		{"worker missing identity", "workers", `[{"worker":"worker-1"}]`, 200, true},
		{"worker null inventory", "workers", `null`, 200, true},
		{"job zero count", "job_count", `0`, 200, false},
		{"job null count", "job_count", `null`, 200, true},
		{"job tag count", "job_tags", `[{"tag":"bun","count":6}]`, 200, false},
		{"queue null suspended", "workspace_queue", `{"database_length":0,"suspended":null}`, 200, false},
		{"queue missing length", "workspace_queue", `{"suspended":0}`, 200, true},
		{"null list", "queue_status", `null`, 200, true},
		{"invalid", "queue_status", `{secret}`, 200, true},
		{"trailing", "queue_counts", `{} {}`, 200, true},
		{"unauthorized", "queue_counts", `secret`, 401, true},
		{"forbidden", "queue_counts", `secret`, 403, true},
		{"unsupported", "queue_counts", `secret`, 404, true},
		{"rate limited", "queue_counts", `secret`, 429, true},
		{"upstream error", "queue_status", `[]`, 503, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if !strings.HasPrefix(request.URL.Path, "/prefix/api/") {
					t.Error(request.URL.Path)
				}
				wantAuth := "Bearer secret"
				if test.collector == "health_status" {
					wantAuth = ""
				}
				if request.Header.Get("Authorization") != wantAuth {
					t.Error("incorrect authorization")
				}
				writer.WriteHeader(test.status)
				_, _ = fmt.Fprint(writer, test.body)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL + "/prefix/")
			client := New(base, "secret", time.Second, 4096)
			defer client.Close()
			_, status, err := client.Get(context.Background(), test.collector, url.Values{"workspace": {"test"}})
			if status != test.status || (err != nil) != test.wantError {
				t.Fatalf("status=%d err=%v", status, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(writer, `{}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := New(base, "secret", time.Second, 4096)
	defer client.Close()
	_, _, err := client.Get(context.Background(), "queue_counts", nil)
	apiError, ok := err.(*APIError)
	if !ok || apiError.Kind != "server_certificate_invalid" {
		t.Fatalf("self-signed TLS certificate was not rejected: %v", err)
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("mode") {
		case "redirect":
			http.Redirect(writer, request, "/elsewhere", http.StatusFound)
		case "wait":
			<-request.Context().Done()
		default:
			_, _ = fmt.Fprint(writer, strings.Repeat(" ", 100)+`{}`)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := New(base, "secret", 50*time.Millisecond, 10)
	defer client.Close()
	for _, mode := range []string{"large", "redirect", "wait"} {
		_, _, err := client.Get(context.Background(), "queue_counts", url.Values{"mode": {mode}})
		if err == nil {
			t.Fatalf("%s succeeded", mode)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.Get(ctx, "queue_counts", nil)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
}
