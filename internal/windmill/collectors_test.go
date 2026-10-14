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

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL + "/prefix")
	client := New(base, "secret", time.Second, 1<<20)
	t.Cleanup(client.Close)
	return client
}

func TestListWorkersPagination(t *testing.T) {
	for _, mode := range []string{"complete", "limit", "duplicate", "failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.URL.Path != "/prefix/api/workers/list" || request.URL.Query().Get("per_page") != "100" {
					t.Error("incorrect paginated request")
				}
				page := request.URL.Query().Get("page")
				if page == "2" && mode == "failure" {
					writer.WriteHeader(403)
					return
				}
				if page == "2" && mode == "complete" {
					_, _ = fmt.Fprint(writer, `[]`)
					return
				}
				rows := []string{}
				count := 100
				if page == "2" {
					count = 1
				}
				for index := 0; index < count; index++ {
					name := fmt.Sprintf("worker-%s-%d", page, index)
					if mode == "duplicate" && page == "2" {
						name = "worker-1-0"
					}
					rows = append(rows, fmt.Sprintf(`{"worker":%q,"worker_instance":"pod","worker_group":"default","started_at":"2026-10-14T00:00:00Z","wm_version":"test","jobs_executed":0,"draining":false}`, name))
				}
				_, _ = fmt.Fprint(writer, "["+strings.Join(rows, ",")+"]")
			})
			limit := 100
			if mode == "duplicate" {
				limit = 101
			}
			rows, _, err := client.ListWorkers(context.Background(), limit)
			if (err == nil) != (mode == "complete") {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
			if mode == "complete" && len(*rows) != 100 {
				t.Fatal("incomplete inventory")
			}
			if err != nil && rows != nil {
				t.Fatal("partial inventory returned")
			}
		})
	}
}

func TestWorkspaceAggregates(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Has("workspace") {
			t.Error("internal workspace parameter leaked")
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/count_jobs"):
			if request.URL.Query().Get("completed_after_s_ago") != "3600" {
				t.Error("incorrect horizon")
			}
			if request.URL.Query().Get("success") == "true" {
				_, _ = fmt.Fprint(writer, "6")
			} else {
				_, _ = fmt.Fprint(writer, "0")
			}
		case strings.HasSuffix(request.URL.Path, "/count_by_tag"):
			if request.URL.Query().Get("workspace_id") != "test" || request.URL.Query().Get("horizon_secs") != "3600" {
				t.Error("incorrect tag scope")
			}
			_, _ = fmt.Fprint(writer, `[{"tag":"bun","count":6}]`)
		case strings.HasSuffix(request.URL.Path, "/queue/count"):
			_, _ = fmt.Fprint(writer, `{"database_length":4,"suspended":2}`)
		default:
			t.Error("unexpected endpoint")
		}
	})
	outcomes, _, err := client.JobOutcomes(context.Background(), []string{"test"}, time.Hour, 100)
	if err != nil || len(*outcomes) != 1 || (*outcomes)[0].Succeeded != 6 || (*outcomes)[0].Failed != 0 {
		t.Fatalf("outcomes=%v err=%v", outcomes, err)
	}
	queues, _, err := client.WorkspaceQueues(context.Background(), []string{"test"})
	if err != nil || (*queues)[0].Inventory.Length.Value != 4 || *(*queues)[0].Inventory.Suspended != 2 {
		t.Fatalf("queues=%v err=%v", queues, err)
	}
}

func TestAggregateFailureAndLimits(t *testing.T) {
	for _, mode := range []string{"failure", "limit", "bad_workspace"} {
		t.Run(mode, func(t *testing.T) {
			client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
				if mode == "failure" && strings.Contains(request.URL.Path, "/second/") {
					writer.WriteHeader(403)
					return
				}
				if strings.HasSuffix(request.URL.Path, "/count_by_tag") {
					_, _ = fmt.Fprint(writer, `[{"tag":"bun","count":6},{"tag":"go","count":1}]`)
				} else {
					_, _ = fmt.Fprint(writer, "6")
				}
			})
			workspaces, limit := []string{"test", "second"}, 100
			if mode == "limit" {
				limit = 1
			}
			if mode == "bad_workspace" {
				workspaces = []string{"../other"}
			}
			rows, _, err := client.JobOutcomes(context.Background(), workspaces, time.Hour, limit)
			if err == nil || rows != nil {
				t.Fatal("invalid/partial outcome data accepted")
			}
		})
	}
}
