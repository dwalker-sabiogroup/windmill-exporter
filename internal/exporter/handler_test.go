package exporter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dwalker-sabiogroup/windmill-exporter/internal/config"
	"github.com/dwalker-sabiogroup/windmill-exporter/internal/windmill"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func fixtureHandler(t *testing.T, collectors string, upstream http.HandlerFunc) *Handler {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	configuration, err := config.Parse(func(key string) string {
		switch key {
		case "WINDMILL_URL":
			return server.URL
		case "WINDMILL_TOKEN":
			return "test-secret"
		case "WINDMILL_COLLECTORS":
			return collectors
		case "OTEL_SERVICE_INSTANCE_ID":
			return "test"
		case "WINDMILL_WORKSPACES":
			return "test"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	client := windmill.New(configuration.URL, configuration.Token, configuration.Timeout, configuration.MaxResponseBytes)
	t.Cleanup(client.Close)
	return NewHandler(configuration, client, slog.New(slog.NewJSONHandler(io.Discard, nil)), "test")
}

func TestTimedProfilingWriteDeadline(t *testing.T) {
	mux := http.NewServeMux()
	RegisterProfiling(mux, true)
	server := httptest.NewUnstartedServer(mux)
	server.Config.WriteTimeout = 500 * time.Millisecond
	server.Start()
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL + "/debug/pprof/profile?seconds=1")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		t.Fatalf("timed profile failed: status=%d bytes=%d err=%v", response.StatusCode, len(body), err)
	}
}

func scrape(handler *Handler) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return response
}

func gatherScrape(t *testing.T, response *httptest.ResponseRecorder) prometheus.Gatherer {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("scrape returned %d: %s", response.Code, response.Body.String())
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	return prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		metrics := make([]*dto.MetricFamily, 0, len(families))
		for _, family := range families {
			metrics = append(metrics, family)
		}
		return metrics, nil
	})
}

func TestScrapeGoAndProcessMetrics(t *testing.T) {
	handler := fixtureHandler(t, "queue_status", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `[]`)
	})
	for range 2 {
		metrics := gatherScrape(t, scrape(handler))
		expectedMetrics := []string{"go_goroutines", "go_memstats_alloc_bytes", "windmill_exporter_scrape_success"}
		if runtime.GOOS == "linux" {
			expectedMetrics = append(expectedMetrics, "process_cpu_seconds_total", "process_resident_memory_bytes", "process_start_time_seconds")
		}
		for _, name := range expectedMetrics {
			if count, err := testutil.GatherAndCount(metrics, name); err != nil || count != 1 {
				t.Errorf("metric %s: count=%d err=%v", name, count, err)
			}
		}
		if problems, err := testutil.GatherAndLint(metrics, expectedMetrics...); err != nil || len(problems) != 0 {
			t.Fatalf("problems=%v err=%v", problems, err)
		}
	}
}

func TestScrapePartialFailureAndDisappearingTags(t *testing.T) {
	var calls atomic.Int64
	handler := fixtureHandler(t, "health_status,queue_status", func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/health/status") {
			writer.WriteHeader(503)
			_, _ = io.WriteString(writer, `{"status":"unhealthy","checked_at":"2026-10-14T00:00:00Z","database_healthy":false,"workers_alive":0}`)
			return
		}
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(writer, `[{"tag":"python","waiting":2,"delay":1.25,"running":1,"workers":3}]`)
		case 2:
			_, _ = io.WriteString(writer, `[]`)
		default:
			writer.WriteHeader(403)
		}
	})
	first := scrape(handler)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	for _, expected := range []string{
		`windmill_health_status{windmill_health_status="unhealthy"} 1`,
		`windmill_database_healthy 0`,
		`windmill_queue_waiting_jobs{windmill_worker_tag="python"} 2`,
		`windmill_queue_delay_seconds{windmill_worker_tag="python"} 1.25`,
		`windmill_exporter_scrape_success 1`,
	} {
		if !strings.Contains(first.Body.String(), expected) {
			t.Errorf("missing %s", expected)
		}
	}
	second := scrape(handler)
	if strings.Contains(second.Body.String(), "windmill_queue_waiting_jobs") {
		t.Fatal("stale tag retained")
	}
	third := scrape(handler)
	if third.Code != 200 || !strings.Contains(third.Body.String(), "windmill_exporter_scrape_success 0") || !strings.Contains(third.Body.String(), `error_type="http_error",windmill_collector="queue_status"} 0`) {
		t.Fatal(third.Body.String())
	}
}

func TestRunnableUnitsAndQuery(t *testing.T) {
	handler := fixtureHandler(t, "runnable_stats", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("limit") != "100" || request.URL.Query().Get("window_secs") != "86400" {
			t.Error("incorrect query")
		}
		_, _ = io.WriteString(writer, `[{"workspace_id":"test","runnable_path":"f/task","worker_group":"default","job_count":2,"total_duration_ms":1500,"max_memory_peak":2048,"sum_memory_peak":3072,"memory_sample_count":2,"total_cpu_ms":750}]`)
	})
	metrics := gatherScrape(t, scrape(handler))
	for _, metric := range []struct {
		name, help, value string
	}{
		{"duration_seconds", "Sum of job durations in the selected window.", "1.5"},
		{"cpu_seconds", "Sum of available job CPU time in the selected window.", "0.75"},
		{"memory_peak_bytes", "Maximum reported job memory peak in the selected window.", "2097152"},
		{"memory_peak_sum_bytes", "Sum of reported positive job memory peaks in the selected window.", "3145728"},
		{"jobs", "Completed job count in the selected hourly-bucket window.", "2"},
	} {
		name := "windmill_runnable_window_" + metric.name
		expected := fmt.Sprintf("# HELP %s %s\n# TYPE %s gauge\n%s{windmill_runnable_path=\"f/task\",windmill_worker_group=\"default\",windmill_workspace_id=\"test\"} %s\n", name, metric.help, name, name, metric.value)
		if err := testutil.GatherAndCompare(metrics, strings.NewReader(expected), name, name+"_total"); err != nil {
			t.Error(err)
		}
	}
}

func TestDetailedOmittedAndDelay(t *testing.T) {
	handler := fixtureHandler(t, "health_detailed,queue_status", func(writer http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "detailed") {
			writer.WriteHeader(503)
			_, _ = io.WriteString(writer, `{"status":"unhealthy","checked_at":"2026-10-14T00:00:00Z","version":"CE test","checks":{"database":{"healthy":false,"latency_ms":5000,"pool":{"size":1,"idle":0,"max_connections":10}},"readiness":{"healthy":false}}}`)
		} else {
			_, _ = io.WriteString(writer, `[{"tag":"python","waiting":0,"running":1,"workers":1}]`)
		}
	})
	response := scrape(handler)
	for _, absent := range []string{"windmill_detailed_workers_active", "windmill_detailed_queue_pending_jobs", "windmill_queue_delay_seconds"} {
		if strings.Contains(response.Body.String(), absent) {
			t.Errorf("unexpected %s", absent)
		}
	}
	if !strings.Contains(response.Body.String(), "windmill_database_latency_seconds 5") {
		t.Fatal(response.Body.String())
	}
}

func TestScrapeConcurrencyAndCancellation(t *testing.T) {
	started := make(chan struct{})
	handler := fixtureHandler(t, "queue_counts", func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	})
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	finished := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), request.WithContext(ctx)); close(finished) }()
	<-started
	if response := scrape(handler); response.Code != 503 {
		t.Fatal(response.Code)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("canceled scrape did not stop")
	}
}

func TestTimeoutHeaderAndMethods(t *testing.T) {
	handler := fixtureHandler(t, "health_status", func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() })
	for _, raw := range []string{"NaN", "Inf", "-1", "bad"} {
		request := httptest.NewRequest("GET", "/metrics", nil)
		request.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", raw)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatal(raw, response.Code)
		}
	}
	request := httptest.NewRequest("GET", "/metrics", nil)
	request.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", "0.05")
	response := httptest.NewRecorder()
	start := time.Now()
	handler.ServeHTTP(response, request)
	if time.Since(start) > time.Second || response.Code != 200 || !strings.Contains(response.Body.String(), "windmill_exporter_scrape_success 0") {
		t.Fatal("timeout not respected")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/metrics", nil))
	if response.Code != 405 {
		t.Fatal(response.Code)
	}
}

func TestPedanticMetrics(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	metrics := healthMetrics("windmill_health", "healthy", 1)
	metrics = append(metrics, waitingMetric("python", 0))
	if err := registry.Register(metrics); err != nil {
		t.Fatal(err)
	}
	problems, err := testutil.GatherAndLint(registry)
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems=%v err=%v", problems, err)
	}
}
