package exporter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeLifecycle(t *testing.T) {
	probes := &Probes{}
	mux := http.NewServeMux()
	probes.Register(mux)
	check := func(path string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d headers=%v", path, response.Code, response.Header())
		}
	}
	check("/startupz", 503)
	check("/readyz", 503)
	check("/livez", 200)
	check("/healthz", 200)
	probes.Start()
	for _, path := range []string{"/startupz", "/readyz", "/livez", "/healthz"} {
		check(path, 200)
	}
	probes.Drain()
	check("/readyz", 503)
	check("/startupz", 200)
	check("/livez", 200)
	check("/healthz", 200)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/readyz", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal("probe accepted a mutating method")
	}
}
