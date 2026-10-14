package exporter

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dwalker-sabiogroup/windmill-exporter/internal/config"
	"github.com/dwalker-sabiogroup/windmill-exporter/internal/windmill"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Handler struct {
	configuration config.Config
	client        *windmill.Client
	logger        *slog.Logger
	version       string
	busy          atomic.Bool
}

func NewHandler(configuration config.Config, client *windmill.Client, logger *slog.Logger, version string) *Handler {
	return &Handler{configuration: configuration, client: client, logger: logger, version: version}
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", "GET")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !handler.busy.CompareAndSwap(false, true) {
		http.Error(writer, "scrape already in progress", http.StatusServiceUnavailable)
		return
	}
	defer handler.busy.Store(false)
	started := time.Now()
	budget := handler.configuration.ScrapeTimeout
	if raw := request.Header.Get("X-Prometheus-Scrape-Timeout-Seconds"); raw != "" {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
			http.Error(writer, "invalid scrape timeout", http.StatusBadRequest)
			return
		}
		if seconds < budget.Seconds() {
			budget = time.Duration(seconds * float64(time.Second))
		}
	}
	reserve := min(100*time.Millisecond, budget/10)
	ctx, cancel := context.WithTimeout(request.Context(), budget-reserve)
	defer cancel()
	metrics := snapshot{targetInfo(handler.configuration, handler.version)}
	var mutex sync.Mutex
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 3)
	success := true
	for _, collector := range handler.configuration.Collectors {
		workers.Go(func() {
			start := time.Now()
			var data any
			var status int
			var err error
			select {
			case semaphore <- struct{}{}:
				switch collector {
				case "workers":
					data, status, err = handler.client.ListWorkers(ctx, handler.configuration.WorkerLimit)
				case "job_outcomes":
					data, status, err = handler.client.JobOutcomes(ctx, handler.configuration.Workspaces, handler.configuration.JobWindow, handler.configuration.JobTagLimit)
				case "workspace_queue":
					data, status, err = handler.client.WorkspaceQueues(ctx, handler.configuration.Workspaces)
				default:
					var query url.Values
					if collector == "runnable_stats" {
						query = handler.configuration.RunnableQuery()
					}
					data, status, err = handler.client.Get(ctx, collector, query)
				}
				<-semaphore
			case <-ctx.Done():
				err = &windmill.APIError{Kind: "timeout"}
			}
			kind := ""
			var collected snapshot
			if err == nil {
				collected = upstreamMetrics(data, handler.configuration)
			} else {
				kind = "collection_error"
				var apiError *windmill.APIError
				if errors.As(err, &apiError) {
					kind = apiError.Kind
				}
				handler.logger.Warn("Windmill collection failed", "windmill_collector", collector, "error_type", kind, "http_response_status_code", status)
			}
			collected = append(collected,
				gauge("windmill_exporter_collector_success", "Whether the enabled collector returned valid data on this scrape.", boolean(err == nil), []string{"windmill_collector", "error_type"}, collector, kind),
				gauge("windmill_exporter_collector_duration_seconds", "Time spent collecting an endpoint, including concurrency wait.", time.Since(start).Seconds(), []string{"windmill_collector"}, collector))
			if status != 0 {
				collected = append(collected, responseMetric(collector, status))
			}
			handler.logger.Debug("Windmill collection completed", "windmill_collector", collector, "success", err == nil)
			mutex.Lock()
			metrics = append(metrics, collected...)
			success = success && err == nil
			mutex.Unlock()
		})
	}
	workers.Wait()
	metrics = append(metrics,
		gauge("windmill_exporter_scrape_success", "Whether all enabled collectors succeeded; not Windmill health.", boolean(success), nil),
		gauge("windmill_exporter_scrape_duration_seconds", "Time spent collecting all enabled endpoints before exposition.", time.Since(started).Seconds(), nil))
	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(metrics); err != nil {
		handler.logger.Error("Metric registration failed")
		http.Error(writer, "metric registration failed", http.StatusInternalServerError)
		return
	}
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{EnableOpenMetrics: true, DisableCompression: true}).ServeHTTP(writer, request)
}
