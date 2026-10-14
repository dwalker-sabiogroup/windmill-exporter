package exporter

import (
	"net/http"
	"sync/atomic"
)

type Probes struct {
	started  atomic.Bool
	draining atomic.Bool
}

func (probes *Probes) Start() { probes.started.Store(true) }

func (probes *Probes) Drain() { probes.draining.Store(true) }

func (probes *Probes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /startupz", func(writer http.ResponseWriter, _ *http.Request) {
		probeResponse(writer, probes.started.Load())
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, _ *http.Request) {
		probeResponse(writer, probes.started.Load() && !probes.draining.Load())
	})
	liveness := func(writer http.ResponseWriter, _ *http.Request) { probeResponse(writer, true) }
	mux.HandleFunc("GET /livez", liveness)
	mux.HandleFunc("GET /healthz", liveness)
}

func probeResponse(writer http.ResponseWriter, healthy bool) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if !healthy {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte("not ready\n"))
		return
	}
	_, _ = writer.Write([]byte("ok\n"))
}
