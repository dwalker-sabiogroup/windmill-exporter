package exporter

import (
	"github.com/dwalker-sabiogroup/windmill-exporter/internal/windmill"
)

func workerMetrics(workers []windmill.Worker) snapshot {
	metrics := snapshot{gauge("windmill_worker_rows", "Workers returned by the complete five-minute-heartbeat inventory.", float64(len(workers)), nil)}
	groups := map[string]int{}
	labels := []string{"windmill_worker", "windmill_worker_group"}
	for _, worker := range workers {
		groups[worker.Group.Value]++
		values := []string{worker.Name.Value, worker.Group.Value}
		metrics = append(metrics,
			gauge("windmill_worker_info", "Worker instance and version identity.", 1, []string{"windmill_worker", "windmill_worker_group", "windmill_worker_instance", "service_version"}, worker.Name.Value, worker.Group.Value, worker.Instance.Value, worker.Version.Value),
			gauge("windmill_worker_started_timestamp_seconds", "Reported worker start time in Unix seconds.", float64(worker.StartedAt.Value.UnixMilli())/1000, labels, values...),
			gauge("windmill_worker_jobs_executed", "Reported worker jobs executed; snapshot, not a guaranteed lifetime counter.", float64(worker.JobsExecuted.Value), labels, values...),
			gauge("windmill_worker_draining", "Whether the worker is draining.", boolean(worker.Draining.Value), labels, values...))
		for _, reading := range []struct {
			name, help string
			value      *int64
			factor     float64
		}{
			{"last_ping_age_seconds", "Age of the worker's last heartbeat as reported by Windmill.", worker.LastPing, 1},
			{"memory_capacity_bytes", "Reported worker memory limit; workers sharing an instance may share this limit.", worker.MemoryCapacity, 1},
			{"memory_usage_bytes", "Reported worker memory usage in bytes.", worker.MemoryUsage, 1},
			{"process_memory_usage_bytes", "Reported Windmill worker process memory usage in bytes.", worker.WindmillMemory, 1},
			{"cpu_quota_seconds", "Cgroup CPU time quota per period; the period is not exposed, so this is not a core count.", worker.CPUQuota, 1e-6},
		} {
			if reading.value != nil {
				metrics = append(metrics, gauge("windmill_worker_"+reading.name, reading.help, float64(*reading.value)*reading.factor, labels, values...))
			}
		}
		for _, reading := range []struct {
			window string
			value  *float64
		}{
			{"lifetime", worker.Occupancy}, {"15s", worker.Occupancy15s}, {"5m", worker.Occupancy5m}, {"30m", worker.Occupancy30m},
		} {
			if reading.value != nil {
				metrics = append(metrics, gauge("windmill_worker_occupancy_ratio", "Reported fraction of worker time spent executing jobs.", *reading.value, []string{"windmill_worker", "windmill_worker_group", "windmill_window"}, worker.Name.Value, worker.Group.Value, reading.window))
			}
		}
	}
	for group, count := range groups {
		metrics = append(metrics, gauge("windmill_worker_group_workers", "Workers seen in the API's five-minute heartbeat window, by group.", float64(count), []string{"windmill_worker_group"}, group))
	}
	return metrics
}

func jobOutcomeMetrics(outcomes windmill.JobOutcomes, windowSeconds float64) snapshot {
	metrics := snapshot{gauge("windmill_jobs_window_seconds", "Configured completed-job lookback.", windowSeconds, nil)}
	for _, row := range outcomes {
		metrics = append(metrics,
			gauge("windmill_jobs_window_completed", "Completed jobs in the lookback by workspace and reported success status.", float64(row.Succeeded), []string{"windmill_workspace_id", "windmill_job_status"}, row.Workspace, "succeeded"),
			gauge("windmill_jobs_window_completed", "Completed jobs in the lookback by workspace and reported success status.", float64(row.Failed), []string{"windmill_workspace_id", "windmill_job_status"}, row.Workspace, "failed"))
		for _, tag := range row.Tags {
			metrics = append(metrics, gauge("windmill_jobs_window_completed_by_tag", "Completed-job counts reported by the tag-count endpoint for the configured workspace and lookback.", float64(tag.Count.Value), []string{"windmill_workspace_id", "windmill_worker_tag"}, row.Workspace, tag.Tag.Value))
		}
	}
	return metrics
}

func workspaceQueueMetrics(queues windmill.WorkspaceQueues) snapshot {
	var metrics snapshot
	for _, row := range queues {
		metrics = append(metrics, gauge("windmill_workspace_queue_jobs", "Workspace queue database row count; not the overdue-by-tag backlog.", float64(row.Inventory.Length.Value), []string{"windmill_workspace_id"}, row.Workspace))
		if row.Inventory.Suspended != nil {
			metrics = append(metrics, gauge("windmill_workspace_suspended_jobs", "Reported suspended jobs in the workspace queue.", float64(*row.Inventory.Suspended), []string{"windmill_workspace_id"}, row.Workspace))
		}
	}
	return metrics
}
