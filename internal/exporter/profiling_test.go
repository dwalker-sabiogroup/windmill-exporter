package exporter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfilingEnablement(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		RegisterProfiling(mux, enabled)
		for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/symbol", "/debug/pprof/goroutine?debug=1", "/debug/pprof/heap"} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			want := http.StatusNotFound
			if enabled {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("enabled=%t %s: status=%d want=%d", enabled, path, response.Code, want)
			}
			if enabled && path == "/debug/pprof/" && !strings.Contains(response.Body.String(), "goroutine") {
				t.Fatal("profiling index did not list runtime profiles")
			}
			if enabled && response.Body.Len() == 0 {
				t.Fatalf("empty profile response: %s", path)
			}
		}
		for _, path := range []string{"/debug/pprof/profile", "/debug/pprof/trace"} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
			want := http.StatusNotFound
			if enabled {
				want = http.StatusMethodNotAllowed
			}
			if response.Code != want {
				t.Fatalf("enabled=%t POST %s: status=%d want=%d", enabled, path, response.Code, want)
			}
		}
	}
}
