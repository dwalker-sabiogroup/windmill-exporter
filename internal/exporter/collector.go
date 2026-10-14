package exporter

import (
	"strconv"

	"github.com/dwalker-sabiogroup/windmill-exporter/internal/config"
	"github.com/dwalker-sabiogroup/windmill-exporter/internal/windmill"
	"github.com/prometheus/client_golang/prometheus"
)

type snapshot []prometheus.Metric

func (metrics snapshot) Describe(descriptors chan<- *prometheus.Desc) {
	for _, metric := range metrics {
		descriptors <- metric.Desc()
	}
}

func (metrics snapshot) Collect(output chan<- prometheus.Metric) {
	for _, metric := range metrics {
		output <- metric
	}
}

func gauge(name, help string, value float64, labels []string, values ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(prometheus.NewDesc(name, help, labels, nil), prometheus.GaugeValue, value, values...)
}

func boolean(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func healthMetrics(prefix, state string, checkedAt float64) snapshot {
	metrics := snapshot{gauge(prefix+"_checked_timestamp_seconds", "Time of the upstream health check in Unix seconds.", checkedAt, nil)}
	for _, phase := range []string{"healthy", "degraded", "unhealthy"} {
		metrics = append(metrics, gauge(prefix+"_status", "Current upstream health phase (one-hot).", boolean(state == phase), []string{"windmill_health_status"}, phase))
	}
	return metrics
}

func upstreamMetrics(data any, configuration config.Config) snapshot {
	var metrics snapshot
	switch result := data.(type) {
	case *windmill.Health:
		metrics = healthMetrics("windmill_health", result.Status.Value, float64(result.CheckedAt.Value.UnixMilli())/1000)
		metrics = append(metrics,
			gauge("windmill_database_healthy", "Whether the status endpoint reports a healthy database.", boolean(result.DatabaseHealthy.Value), nil),
			gauge("windmill_workers_alive", "Workers with a heartbeat in the status endpoint's five-minute window.", float64(result.WorkersAlive.Value), nil))
	case *windmill.Detailed:
		metrics = healthMetrics("windmill_detailed_health", result.Status.Value, float64(result.CheckedAt.Value.UnixMilli())/1000)
		checks := result.Checks.Value
		database := checks.Database.Value
		pool := database.Pool.Value
		metrics = append(metrics,
			gauge("windmill_instance_info", "Upstream Windmill instance version.", 1, []string{"service_name", "service_version"}, "windmill", result.Version.Value),
			gauge("windmill_detailed_database_healthy", "Whether the detailed endpoint reports a healthy database.", boolean(database.Healthy.Value), nil),
			gauge("windmill_database_latency_seconds", "Duration of the latest detailed database health query.", float64(database.LatencyMS.Value)/1000, nil),
			gauge("windmill_database_pool_connections", "Current connections in the queried API server's database pool.", float64(pool.Size.Value), nil),
			gauge("windmill_database_pool_idle_connections", "Idle connections in the queried API server's database pool.", float64(pool.Idle.Value), nil),
			gauge("windmill_database_pool_max_connections", "Maximum connections in the queried API server's database pool.", float64(pool.MaxConnections.Value), nil),
			gauge("windmill_readiness_healthy", "Whether the queried API server reports readiness.", boolean(checks.Readiness.Value.Healthy.Value), nil))
		if checks.Workers != nil {
			metrics = append(metrics,
				gauge("windmill_detailed_workers_healthy", "Whether the detailed endpoint reports healthy workers.", boolean(checks.Workers.Healthy.Value), nil),
				gauge("windmill_detailed_workers_active", "Workers with a heartbeat in the detailed endpoint's five-minute window.", float64(checks.Workers.ActiveCount.Value), nil))
		}
		if checks.Queue != nil {
			metrics = append(metrics,
				gauge("windmill_detailed_queue_pending_jobs", "Instance-wide jobs overdue by more than three seconds and not running.", float64(checks.Queue.PendingJobs.Value), nil),
				gauge("windmill_detailed_queue_running_jobs", "Instance-wide running jobs reported by detailed health.", float64(checks.Queue.RunningJobs.Value), nil))
		}
	case *[]windmill.Tag:
		for _, tag := range *result {
			metrics = append(metrics,
				waitingMetric(tag.Tag.Value, tag.Waiting.Value),
				gauge("windmill_queue_running_jobs", "Running jobs by worker tag.", float64(tag.Running.Value), []string{"windmill_worker_tag"}, tag.Tag.Value),
				gauge("windmill_queue_workers", "Workers serving this tag with a heartbeat in the last minute; not unique across tags.", float64(tag.Workers.Value), []string{"windmill_worker_tag"}, tag.Tag.Value))
			if tag.Delay != nil {
				metrics = append(metrics, gauge("windmill_queue_delay_seconds", "Wait of the next job selected by worker pull priority.", *tag.Delay, []string{"windmill_worker_tag"}, tag.Tag.Value))
			}
		}
	case *map[string]windmill.Required[int64]:
		for tag, count := range *result {
			metrics = append(metrics, waitingMetric(tag, count.Value))
		}
	case *[]windmill.Runnable:
		labels := []string{"windmill_workspace_id", "windmill_runnable_path", "windmill_worker_group"}
		for _, runnable := range *result {
			values := []string{runnable.Workspace.Value, runnable.Path.Value, runnable.WorkerGroup.Value}
			for _, metric := range []struct {
				name, help string
				value      float64
			}{
				{"jobs", "Completed job count in the selected hourly-bucket window.", float64(runnable.Jobs.Value)},
				{"duration_seconds", "Sum of job durations in the selected window.", float64(runnable.DurationMS.Value) / 1000},
				{"cpu_seconds", "Sum of available job CPU time in the selected window.", float64(runnable.CPUMS.Value) / 1000},
				{"memory_peak_bytes", "Maximum reported job memory peak in the selected window.", float64(runnable.MemoryPeak.Value) * 1024},
				{"memory_peak_sum_bytes", "Sum of reported positive job memory peaks in the selected window.", float64(runnable.MemorySum.Value) * 1024},
				{"memory_samples", "Jobs with a positive memory reading in the selected window.", float64(runnable.MemorySamples.Value)},
			} {
				metrics = append(metrics, gauge("windmill_runnable_window_"+metric.name, metric.help, metric.value, labels, values...))
			}
		}
		metrics = append(metrics,
			gauge("windmill_runnable_window_seconds", "Configured lookback; the starting hourly bucket is included whole.", configuration.RunnableWindow.Seconds(), nil),
			gauge("windmill_runnable_rows", "Returned top-N runnable rows; not a complete inventory.", float64(len(*result)), nil),
			gauge("windmill_runnable_limit", "Configured top-N row limit.", float64(configuration.RunnableLimit), nil))
	case *[]windmill.Worker:
		metrics = workerMetrics(*result)
	case *windmill.JobOutcomes:
		metrics = jobOutcomeMetrics(*result, configuration.JobWindow.Seconds())
	case *windmill.WorkspaceQueues:
		metrics = workspaceQueueMetrics(*result)
	}
	return metrics
}

func waitingMetric(tag string, count int64) prometheus.Metric {
	return gauge("windmill_queue_waiting_jobs", "Jobs overdue by more than three seconds and not running, by worker tag.", float64(count), []string{"windmill_worker_tag"}, tag)
}

func targetInfo(configuration config.Config, version string) prometheus.Metric {
	labels := []string{"service_name", "service_instance_id", "service_version"}
	values := []string{configuration.ServiceName, configuration.ServiceInstanceID, version}
	if configuration.ServiceNamespace != "" {
		labels = append(labels, "service_namespace")
		values = append(values, configuration.ServiceNamespace)
	}
	if configuration.Environment != "" {
		labels = append(labels, "deployment_environment_name")
		values = append(values, configuration.Environment)
	}
	return gauge("target_info", "Exporter resource identity using OpenTelemetry attribute names translated to Prometheus labels.", 1, labels, values...)
}

func responseMetric(collector string, status int) prometheus.Metric {
	return gauge("windmill_exporter_response_status_code", "HTTP status received from the Windmill endpoint on this scrape.", float64(status), []string{"windmill_collector", "http_request_method", "http_response_status_code"}, collector, "GET", strconv.Itoa(status))
}
