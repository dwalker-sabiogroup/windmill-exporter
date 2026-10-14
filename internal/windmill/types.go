package windmill

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

type Required[T any] struct {
	Value   T
	Present bool
}

func (field *Required[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("required field is null")
	}
	if err := json.Unmarshal(data, &field.Value); err != nil {
		return err
	}
	field.Present = true
	return nil
}

type Health struct {
	Status          Required[string]    `json:"status"`
	CheckedAt       Required[time.Time] `json:"checked_at"`
	DatabaseHealthy Required[bool]      `json:"database_healthy"`
	WorkersAlive    Required[int64]     `json:"workers_alive"`
}

type Detailed struct {
	Status    Required[string]    `json:"status"`
	CheckedAt Required[time.Time] `json:"checked_at"`
	Version   Required[string]    `json:"version"`
	Checks    Required[Checks]    `json:"checks"`
}

type Checks struct {
	Database  Required[Database]  `json:"database"`
	Workers   *Workers            `json:"workers"`
	Queue     *Queue              `json:"queue"`
	Readiness Required[Readiness] `json:"readiness"`
}

type Database struct {
	Healthy   Required[bool]  `json:"healthy"`
	LatencyMS Required[int64] `json:"latency_ms"`
	Pool      Required[Pool]  `json:"pool"`
}

type Pool struct {
	Size           Required[int64] `json:"size"`
	Idle           Required[int64] `json:"idle"`
	MaxConnections Required[int64] `json:"max_connections"`
}

type Workers struct {
	Healthy     Required[bool]  `json:"healthy"`
	ActiveCount Required[int64] `json:"active_count"`
}

type Queue struct {
	PendingJobs Required[int64] `json:"pending_jobs"`
	RunningJobs Required[int64] `json:"running_jobs"`
}

type Readiness struct {
	Healthy Required[bool] `json:"healthy"`
}

type Tag struct {
	Tag     Required[string] `json:"tag"`
	Waiting Required[int64]  `json:"waiting"`
	Delay   *float64         `json:"delay"`
	Running Required[int64]  `json:"running"`
	Workers Required[int64]  `json:"workers"`
}

type Runnable struct {
	Workspace     Required[string] `json:"workspace_id"`
	Path          Required[string] `json:"runnable_path"`
	WorkerGroup   Required[string] `json:"worker_group"`
	Jobs          Required[int64]  `json:"job_count"`
	DurationMS    Required[int64]  `json:"total_duration_ms"`
	MemoryPeak    Required[int64]  `json:"max_memory_peak"`
	MemorySum     Required[int64]  `json:"sum_memory_peak"`
	MemorySamples Required[int64]  `json:"memory_sample_count"`
	CPUMS         Required[int64]  `json:"total_cpu_ms"`
}

type Worker struct {
	Name           Required[string]    `json:"worker"`
	Instance       Required[string]    `json:"worker_instance"`
	Group          Required[string]    `json:"worker_group"`
	StartedAt      Required[time.Time] `json:"started_at"`
	Version        Required[string]    `json:"wm_version"`
	JobsExecuted   Required[int64]     `json:"jobs_executed"`
	Draining       Required[bool]      `json:"draining"`
	LastPing       *int64              `json:"last_ping"`
	Occupancy      *float64            `json:"occupancy_rate"`
	Occupancy15s   *float64            `json:"occupancy_rate_15s"`
	Occupancy5m    *float64            `json:"occupancy_rate_5m"`
	Occupancy30m   *float64            `json:"occupancy_rate_30m"`
	MemoryCapacity *int64              `json:"memory"`
	MemoryUsage    *int64              `json:"memory_usage"`
	WindmillMemory *int64              `json:"wm_memory_usage"`
	CPUQuota       *int64              `json:"vcpus"`
}

type CompletedTag struct {
	Tag   Required[string] `json:"tag"`
	Count Required[int64]  `json:"count"`
}

type QueueInventory struct {
	Length    Required[int64] `json:"database_length"`
	Suspended *int64          `json:"suspended"`
}

type JobOutcome struct {
	Workspace string
	Succeeded int64
	Failed    int64
	Tags      []CompletedTag
}

type JobOutcomes []JobOutcome

type WorkspaceQueue struct {
	Workspace string
	Inventory QueueInventory
}

type WorkspaceQueues []WorkspaceQueue
