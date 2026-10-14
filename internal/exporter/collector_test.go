package exporter

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestQueueCountsExposition(t *testing.T) {
	handler := fixtureHandler(t, "queue_counts", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/workers/queue_counts" {
			t.Error("unexpected endpoint")
		}
		_, _ = io.WriteString(writer, `{"python":0,"go":3}`)
	})
	response := scrape(handler)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `windmill_queue_waiting_jobs{windmill_worker_tag="python"} 0`) || !strings.Contains(response.Body.String(), `windmill_queue_waiting_jobs{windmill_worker_tag="go"} 3`) {
		t.Fatal(response.Body.String())
	}
	if strings.Contains(response.Body.String(), "windmill_queue_running_jobs") {
		t.Fatal("queue counts synthesized running data")
	}
	expected := `# HELP windmill_queue_waiting_jobs Jobs overdue by more than three seconds and not running, by worker tag.
# TYPE windmill_queue_waiting_jobs gauge
windmill_queue_waiting_jobs{windmill_worker_tag="python"} 0
`
	if err := testutil.CollectAndCompare(snapshot{waitingMetric("python", 0)}, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestOperationalPartialFailures(t *testing.T) {
	var generation atomic.Int64
	generation.Store(1)
	handler := fixtureHandler(t, "workers,workspace_queue", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/workers/list" {
			if generation.Load() == 1 {
				_, _ = io.WriteString(writer, `[{"worker":"worker-1","worker_instance":"pod-1","worker_group":"default","started_at":"2026-10-14T00:00:00Z","wm_version":"v1.833.0","jobs_executed":0,"draining":true}]`)
			} else {
				_, _ = io.WriteString(writer, `[]`)
			}
		} else if generation.Load() == 1 {
			_, _ = io.WriteString(writer, `{"database_length":0,"suspended":null}`)
		} else {
			writer.WriteHeader(403)
		}
	})
	first := scrape(handler)
	if first.Code != 200 || !strings.Contains(first.Body.String(), `windmill_worker_draining{windmill_worker="worker-1",windmill_worker_group="default"} 1`) {
		t.Fatal(first.Body.String())
	}
	for _, absent := range []string{"windmill_worker_memory_usage_bytes", "windmill_worker_last_ping_age_seconds", "windmill_workspace_suspended_jobs"} {
		if strings.Contains(first.Body.String(), absent) {
			t.Fatalf("synthesized missing reading %s", absent)
		}
	}
	generation.Store(2)
	second := scrape(handler)
	if second.Code != 200 || !strings.Contains(second.Body.String(), "windmill_worker_rows 0") || !strings.Contains(second.Body.String(), "windmill_exporter_scrape_success 0") {
		t.Fatal(second.Body.String())
	}
	for _, absent := range []string{"windmill_worker_info", "windmill_worker_group_workers", "windmill_workspace_queue_jobs"} {
		if strings.Contains(second.Body.String(), absent) {
			t.Fatalf("stale data retained: %s", absent)
		}
	}
}

func TestOperationalExposition(t *testing.T) {
	handler := fixtureHandler(t, "workers,job_outcomes,workspace_queue", func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/api/workers/list":
			_, _ = io.WriteString(writer, `[{"worker":"worker-1","worker_instance":"pod-1","worker_group":"default","started_at":"2026-10-14T00:00:00Z","wm_version":"v1.833.0","jobs_executed":51,"draining":false,"last_ping":4,"occupancy_rate_5m":0.25,"memory":2147483648,"memory_usage":40509440,"wm_memory_usage":26034176,"vcpus":30000}]`)
		case strings.HasSuffix(request.URL.Path, "/count_jobs"):
			if request.URL.Query().Get("success") == "true" {
				_, _ = io.WriteString(writer, "6")
			} else {
				_, _ = io.WriteString(writer, "0")
			}
		case strings.HasSuffix(request.URL.Path, "/count_by_tag"):
			_, _ = io.WriteString(writer, `[{"tag":"bun","count":6}]`)
		case strings.HasSuffix(request.URL.Path, "/queue/count"):
			_, _ = io.WriteString(writer, `{"database_length":4,"suspended":2}`)
		default:
			t.Error("unexpected API request")
		}
	})
	response := scrape(handler)
	for _, expected := range []string{
		`windmill_worker_last_ping_age_seconds{windmill_worker="worker-1",windmill_worker_group="default"} 4`,
		`windmill_worker_memory_capacity_bytes{windmill_worker="worker-1",windmill_worker_group="default"} 2.147483648e+09`,
		`windmill_worker_cpu_quota_seconds{windmill_worker="worker-1",windmill_worker_group="default"} 0.03`,
		`windmill_worker_occupancy_ratio{windmill_window="5m",windmill_worker="worker-1",windmill_worker_group="default"} 0.25`,
		`windmill_jobs_window_completed{windmill_job_status="succeeded",windmill_workspace_id="test"} 6`,
		`windmill_jobs_window_completed{windmill_job_status="failed",windmill_workspace_id="test"} 0`,
		`windmill_jobs_window_completed_by_tag{windmill_worker_tag="bun",windmill_workspace_id="test"} 6`,
		`windmill_workspace_queue_jobs{windmill_workspace_id="test"} 4`,
		`windmill_workspace_suspended_jobs{windmill_workspace_id="test"} 2`,
		`windmill_exporter_scrape_success 1`,
		`# TYPE windmill_jobs_window_completed gauge`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("missing %s\n%s", expected, response.Body.String())
		}
	}
	if response.Code != 200 || strings.Contains(response.Body.String(), "windmill_worker_occupancy_ratio{windmill_window=\"15s\"") {
		t.Fatal("missing optional reading was synthesized")
	}
}
