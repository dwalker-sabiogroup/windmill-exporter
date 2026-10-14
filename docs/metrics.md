# Metrics

All Windmill measurements are **gauges** representing the latest API response.
Metric names use seconds and bytes; absent data remains absent. Tags/runnables
that disappear do not persist across scrapes. No source timestamps are attached
to samples; the health check timestamp is a separate gauge.

## Catalogue

| Metric | Meaning |
| --- | --- |
| `target_info` | Exporter identity; value 1 |
| `windmill_instance_info` | Upstream version; value 1 |
| `windmill_health_status` | One-hot `healthy`, `degraded`, `unhealthy` |
| `windmill_health_checked_timestamp_seconds` | Status check time in Unix seconds |
| `windmill_database_healthy` | Status database health, 0/1 |
| `windmill_workers_alive` | Status workers seen within five minutes |
| `windmill_detailed_health_status` | Detailed one-hot health phase |
| `windmill_detailed_health_checked_timestamp_seconds` | Detailed check time |
| `windmill_detailed_database_healthy` | Detailed database health, 0/1 |
| `windmill_database_latency_seconds` | Latest detailed database query duration |
| `windmill_database_pool_connections` | Current API-server pool connections |
| `windmill_database_pool_idle_connections` | Idle API-server pool connections |
| `windmill_database_pool_max_connections` | Configured API-server pool maximum |
| `windmill_readiness_healthy` | Queried API server readiness, 0/1 |
| `windmill_detailed_workers_healthy` | Detailed worker health, 0/1 |
| `windmill_detailed_workers_active` | Detailed workers seen within five minutes |
| `windmill_detailed_queue_pending_jobs` | Instance-wide overdue, nonrunning jobs |
| `windmill_detailed_queue_running_jobs` | Instance-wide running jobs |
| `windmill_queue_waiting_jobs` | Jobs overdue by more than three seconds, by tag |
| `windmill_queue_running_jobs` | Running jobs, by tag |
| `windmill_queue_delay_seconds` | Next worker-pull job's wait, by tag |
| `windmill_queue_workers` | Workers serving a tag, heartbeat within one minute |
| `windmill_runnable_window_jobs` | Completed job count in selected window |
| `windmill_runnable_window_duration_seconds` | Sum of job durations in window |
| `windmill_runnable_window_cpu_seconds` | Sum of available CPU time in window |
| `windmill_runnable_window_memory_peak_bytes` | Maximum reported memory peak |
| `windmill_runnable_window_memory_peak_sum_bytes` | Sum of positive memory peaks |
| `windmill_runnable_window_memory_samples` | Jobs with positive memory readings |
| `windmill_runnable_window_seconds` | Configured lookback |
| `windmill_runnable_rows` | Returned top-N row count |
| `windmill_runnable_limit` | Configured top-N limit |
| `windmill_worker_rows` | Complete inventory of workers seen within five minutes |
| `windmill_worker_info` | Worker instance and version identity |
| `windmill_worker_started_timestamp_seconds` | Reported worker start time |
| `windmill_worker_jobs_executed` | Reported executed jobs; snapshot, not a lifetime counter |
| `windmill_worker_draining` | Worker draining state, 0/1 |
| `windmill_worker_last_ping_age_seconds` | Reported heartbeat age |
| `windmill_worker_occupancy_ratio` | Execution occupancy for lifetime/15s/5m/30m windows |
| `windmill_worker_memory_capacity_bytes` | Reported memory limit |
| `windmill_worker_memory_usage_bytes` | Reported worker memory usage |
| `windmill_worker_process_memory_usage_bytes` | Reported Windmill process memory usage |
| `windmill_worker_cpu_quota_seconds` | CPU time budget per cgroup period, not cores |
| `windmill_worker_group_workers` | Workers by group in the five-minute heartbeat window |
| `windmill_jobs_window_seconds` | Configured completed-job lookback |
| `windmill_jobs_window_completed` | Successful/unsuccessful jobs in the lookback by workspace |
| `windmill_jobs_window_completed_by_tag` | Tag-count endpoint's completed jobs by workspace/tag |
| `windmill_workspace_queue_jobs` | Reported workspace queue database rows |
| `windmill_workspace_suspended_jobs` | Reported workspace suspended jobs |
| `windmill_exporter_collector_success` | Enabled collector returned valid data, 0/1 |
| `windmill_exporter_collector_duration_seconds` | Collection and concurrency-wait duration |
| `windmill_exporter_response_status_code` | Last HTTP response code, if received |
| `windmill_exporter_scrape_success` | All enabled collectors succeeded, 0/1 |
| `windmill_exporter_scrape_duration_seconds` | Collection time before exposition |

Health labels: `windmill_health_status`. Queue labels: `windmill_worker_tag`.
Runnable labels: `windmill_workspace_id`, `windmill_runnable_path`,
`windmill_worker_group`. Collector diagnostics use `windmill_collector`;
collector success also uses `error_type` (empty on success). HTTP response
diagnostics use `http_request_method` and `http_response_status_code`; there is
no fabricated status code for transport failures.

Worker readings use `windmill_worker` and `windmill_worker_group`; worker info
also includes `windmill_worker_instance` and `service_version`. Occupancy uses
the bounded `windmill_window` label (`lifetime`, `15s`, `5m`, `30m`). Job and
workspace queue metrics use `windmill_workspace_id`; outcomes additionally use
`windmill_job_status` (`succeeded`, `failed`), and tag counts use `windmill_worker_tag`.

Error types: `http_error`, `timeout`, `canceled`, `transport_error`,
`server_certificate_invalid`, `response_too_large`, `invalid_response`,
`invalid_request`, `unknown_collector`, `worker_limit_exceeded`,
`job_tag_limit_exceeded`, and defensive `collection_error`.

## Interpretation

Queue endpoints are instance-wide, not workspace-specific. Future-scheduled
jobs and jobs less than three seconds overdue are excluded from waiting counts.
Delay respects worker pull priority, so it is not necessarily the oldest job.
Missing/null delay is omitted. Worker counts overlap across tags and must not be
summed as unique workers. Tags without waiting/running jobs may be absent.
`queue_counts` emits only waiting gauges and is an alternative to `queue_status`.

Status may use Windmill's short-lived health cache; detailed checks are fresh.
Database pool/readiness belong to the particular API server handling the request,
not a sum across replicas. Separate endpoint requests are not an atomic snapshot.
Detailed worker/queue checks may be omitted when the database is unhealthy.
Some Windmill queue helpers return empty counts on database errors; the exporter
cannot detect a failure the API itself masks.

Runnable statistics use asynchronously flushed hourly buckets with 30-day
retention. The bucket containing the lookback start is included whole. Responses
are top-N by duration/memory/CPU, not an inventory, and have no truncation flag.
Counts and sums can decrease as the window advances or rows leave the top-N.
**Do not use `rate()` or treat these gauges as counters or distributions.**
Mean duration can be computed as window duration divided by window jobs, filtering
out zero jobs. Mean sampled peak is peak sum divided by memory samples, filtering
out zero samples. These are not mean process RSS measurements.

Memory is converted from Windmill's KiB-style worker readings using 1024 bytes per
unit. Some in-process executors have no CPU reading and report no memory or only
result size. These are available resource readings, not complete accounting.
Runnable paths and worker tags can have high cardinality; keep runnable metrics
opt-in, filter by workspace/group, and cap the row limit.

Worker inventory is paginated completely within `WINDMILL_WORKER_LIMIT`. The API
omits workers without a heartbeat in the last five minutes, so dead workers
eventually disappear rather than remaining as zero-valued series. Version, start
time and draining state are always exposed for valid worker rows; missing optional
heartbeat/resource/occupancy readings are omitted. Memory values in this endpoint
are already bytes, unlike runnable memory statistics. Workers sharing an instance
can share memory limits/readings: do not sum them into unique pod capacity.
`vcpus` is the cgroup quota in microseconds, converted to seconds. The API does not
provide the period needed to calculate cores; obtain CPU capacity and throttling
from Kubernetes/cAdvisor instead. Occupancy is a fraction, not a percentage.

Job outcomes are window gauges, not cumulative counters. Successful/unsuccessful
counts and tag counts use separate non-atomic queries and may have different
endpoint visibility; do not add the two breakdowns together or assume exact
equality during activity. `failed` means `success=false`, including unsuccessful
jobs such as cancellations where the API reports them. These queries do not
create distributions or percentiles. A failure or row-limit violation omits the
entire affected collector, including previously successful workspace rows; other
collectors still report their own valid data.

Workspace queue counts describe database rows, not only jobs overdue by three
seconds; suspended jobs are reported separately. Null/absent suspended counts
are omitted, not converted to zero. Only explicit `WINDMILL_WORKSPACES` are queried;
the exporter does not auto-discover and scan every workspace.

## OpenTelemetry alignment and GenAI

Applicable attribute names are translated from dots to underscores:
`service.name` -> `service_name`, `service.instance.id` -> `service_instance_id`,
`service.version` -> `service_version`, `service.namespace` -> `service_namespace`,
and `deployment.environment.name` -> `deployment_environment_name`. Exporter
resource identity appears only on `target_info`; upstream version identity uses
`windmill_instance_info`. Prometheus scrape labels distinguish monitored instances.
No OTLP exporter or OTel SDK is required. Unmatched Windmill concepts use custom
`windmill_*` labels rather than pretending to be messaging or GenAI operations.

GenAI conventions are development-status and now maintained in a separate
repository. Health/queue/runnable data has no token, provider, or model information.
An optional future community source is
`GET /api/w/WORKSPACE/ai/usage?days=N&group_by=model&scope=workspace`.
The reader and client-reported usage writer have no enterprise gate in the
inspected source. Workspace totals require workspace-admin access. This reports
UI-chat usage, not comprehensive AI-agent flow steps or provider billing.
It returns bounded calendar-day sums of input/output/cache tokens, request counts,
and optional provider-reported nano-USD amounts. Its 1000-bucket cap has an explicit
`truncated` flag. These rolling totals cannot be exposed as standard cumulative
GenAI usage counters or reconstructed per-operation histograms. AI metrics are
deliberately deferred; a future collector would use real values, bounded labels,
correct window semantics, and no guessed costs/providers/models.

## Sources

Contracts checked on 2026-10-14 against upstream source and authenticated
CE v1.833.0. All seven complementary collectors returned successful data on that
instance; confirm compatibility and permissions with your installed version.

- [Windmill OpenAPI](https://app.windmill.dev/openapi.html)
- [Health handlers](https://github.com/windmill-labs/windmill/blob/main/backend/windmill-api/src/health.rs)
- [Worker handlers](https://github.com/windmill-labs/windmill/blob/main/backend/windmill-api-workers/src/lib.rs)
- [Queue definitions](https://github.com/windmill-labs/windmill/blob/main/backend/windmill-common/src/queue.rs)
- [Runnable aggregation](https://github.com/windmill-labs/windmill/blob/main/backend/windmill-common/src/runnable_job_stats.rs)
- [Worker memory readings](https://github.com/windmill-labs/windmill/blob/main/backend/windmill-worker/src/handle_child.rs)
- [Worker CPU quota definitions](https://github.com/windmill-labs/windmill/blob/v1.833.0/backend/windmill-common/src/worker.rs)
- [AI usage handlers](https://github.com/windmill-labs/windmill/blob/main/backend/windmill-api/src/ai.rs)
- [OTel HTTP conventions](https://opentelemetry.io/docs/specs/semconv/http/http-metrics/)
- [OTel service identity](https://opentelemetry.io/docs/specs/semconv/resource/service/)
- [GenAI conventions](https://github.com/open-telemetry/semantic-conventions-genai)