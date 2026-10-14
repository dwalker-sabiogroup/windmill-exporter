# windmill-exporter

A small Prometheus exporter for Windmill Community Edition. It queries one
instance on each scrape, uses environment-only configuration, and writes JSON
logs with Go's `slog`.

## Run locally

Requires Go 1.27.2, or Docker on Linux amd64. From the repository root:

```sh
export WINDMILL_URL=https://windmill.example.com
# Set WINDMILL_TOKEN in your shell using your normal secret-management method.
go run ./cmd/windmill-exporter
```

In another terminal:

```sh
curl --fail http://localhost:9108/healthz
curl --fail http://localhost:9108/metrics
```

The default collectors require a bearer token belonging to an instance-level
**DevOps** user. Use a dedicated monitoring user and keep its token out of git,
shell history, logs, and chat. A workspace-admin role alone is insufficient;
ordinary job tokens are unsuitable. Superadmin also works but grants more access
than necessary.

To test public health without a token:

```sh
WINDMILL_URL=https://windmill.example.com WINDMILL_COLLECTORS=health_status \
	go run ./cmd/windmill-exporter
```

### Docker

```sh
docker build --platform linux/amd64 -t windmill-exporter:local .
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
	-p 127.0.0.1:9108:9108 -e WINDMILL_URL -e WINDMILL_TOKEN windmill-exporter:local
```

Published images: `ghcr.io/dwalker-sabiogroup/windmill-exporter:VERSION`.
Images support **linux/amd64 only**, use a static binary, run as UID/GID 65532,
and contain no shell. Container `localhost` is not your host; for a host-local
Windmill on Linux, use `--add-host=host.docker.internal:host-gateway` and set the
URL accordingly. An `.env` file is not loaded automatically; `docker --env-file`
or your shell can supply environment variables.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `WINDMILL_URL` | Required | Instance root, including any deployment prefix; not `/api` |
| `WINDMILL_TOKEN` | Required for authenticated collectors | Bearer token |
| `WINDMILL_COLLECTORS` | `health_status,health_detailed,queue_status` | Comma-separated enabled collectors |
| `WINDMILL_TIMEOUT` | `5s` | Per-request timeout |
| `EXPORTER_SCRAPE_TIMEOUT` | `10s` | Overall collection timeout; greater than request timeout, at most `5m` |
| `EXPORTER_LISTEN_ADDRESS` | `:9108` | Listening host and port |
| `EXPORTER_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `EXPORTER_MAX_RESPONSE_BYTES` | `4194304` | Limit per response; maximum 64 MiB |
| `WINDMILL_RUNNABLE_WINDOW` | `24h` | Whole-second lookback between `1h` and `720h` |
| `WINDMILL_RUNNABLE_LIMIT` | `100` | Top-N runnable rows, between 1 and 1000 |
| `WINDMILL_RUNNABLE_WORKSPACE` | Unset | Optional runnable workspace filter |
| `WINDMILL_RUNNABLE_WORKER_GROUP` | Unset | Optional runnable worker-group filter |
| `WINDMILL_RUNNABLE_ORDER_BY` | `total_duration` | `total_duration`, `max_memory`, `cpu` |
| `WINDMILL_WORKSPACES` | Unset | Explicit comma-separated workspace IDs for `job_outcomes`/`workspace_queue`, maximum 20 |
| `WINDMILL_WORKER_LIMIT` | `1000` | Maximum complete worker inventory, between 1 and 10000 |
| `WINDMILL_JOB_WINDOW` | `1h` | Whole-second job-outcome lookback between `1m` and `24h` |
| `WINDMILL_JOB_TAG_LIMIT` | `100` | Maximum returned job tags per workspace, between 1 and 1000 |
| `OTEL_SERVICE_NAME` | `windmill-exporter` | Exporter identity on `target_info` |
| `OTEL_SERVICE_NAMESPACE` | Unset | Optional exporter service namespace |
| `OTEL_SERVICE_INSTANCE_ID` | Process-lifetime UUID | Unique exporter instance identity |
| `OTEL_DEPLOYMENT_ENVIRONMENT_NAME` | Unset | Optional environment identity |

Configuration is validated at startup. Unknown or duplicate collectors fail;
`queue_status` and `queue_counts` are mutually exclusive. Go's standard
`SSL_CERT_FILE`/`SSL_CERT_DIR` environment variables can select mounted private CA
certificates. TLS verification is never disabled. HTTP is supported for trusted
local networks, with a warning if a token would be sent unencrypted.

Enable optional runnable metrics explicitly:

```sh
export WINDMILL_COLLECTORS=health_status,health_detailed,queue_status,runnable_stats
export WINDMILL_RUNNABLE_WORKSPACE=my-workspace
go run ./cmd/windmill-exporter
```

Enable all operational collectors for the tested workspace:

```sh
export WINDMILL_COLLECTORS=health_status,health_detailed,queue_status,runnable_stats,workers,job_outcomes,workspace_queue
export WINDMILL_WORKSPACES=ai
export WINDMILL_JOB_WINDOW=1h
go run ./cmd/windmill-exporter
```

Use your own workspace IDs, separated by commas. This list is independent of the
optional runnable workspace filter. The new collectors are opt-in; the original
default list is unchanged. Restart the process after changing environment settings.
Worker enumeration uses pages of at most 100 rows and fails rather than emitting
a partial inventory when its limit is exceeded. Job outcomes require three
sequential reads per workspace (success, failure, and tags); workspace queue adds
one more. Limit workspace count and lookback on large instances, and adjust the
overall scrape/Prometheus timeouts if necessary. No jobs, logs or results are scanned.

## Endpoints and compatibility

| Collector | Endpoint | Authentication |
| --- | --- | --- |
| `health_status` | `/api/health/status` | None |
| `health_detailed` | `/api/health/detailed` | Authenticated user |
| `queue_status` | `/api/workers/queue_status` | Instance DevOps |
| `queue_counts` | `/api/workers/queue_counts` | Instance DevOps |
| `runnable_stats` | `/api/workers/runnable_stats` | Instance DevOps |
| `workers` | `/api/workers/list` | Authenticated user; DevOps for full visibility |
| `job_outcomes` | `/api/w/WORKSPACE/jobs/completed/count_jobs` and `/api/jobs/completed/count_by_tag` | Token with endpoint-specific job-read permissions |
| `workspace_queue` | `/api/w/WORKSPACE/jobs/queue/count` | Access to the configured workspace |

These handlers and runnable aggregation were checked against the community source
and tested with an authenticated CE v1.833.0 instance on 2026-10-14. Job and queue
counts reflect the token's permitted view; a 403 is a collection failure, not zero
activity. Older releases may not implement these endpoints. Missing endpoints are reported
as collection failures, not silently disabled or replaced. No enterprise metrics
endpoint is used. The checked contracts and metric meanings are documented in
[docs/metrics.md](docs/metrics.md).

## Prometheus and GKE

```yaml
scrape_configs:
	- job_name: windmill
		scrape_interval: 15s
		scrape_timeout: 12s
		static_configs:
			- targets: [windmill-exporter:9108]
```

In GKE, inject `WINDMILL_TOKEN` from a Kubernetes Secret, run on amd64 nodes, and
use `/healthz` on port 9108 for probes. `/healthz` checks exporter liveness only.
Keep `/metrics` private: the exporter listener has no inbound authentication or
TLS, and metrics may reveal worker tags and runnable paths. Use network policies
or an authenticated proxy where appropriate. One active scrape is allowed per
process; overlapping scrapes receive HTTP 503. API requests are bounded to three
concurrent requests, use no retries, and honor a shorter Prometheus timeout.

Prometheus `up` means the exporter was reachable. Alert on
`windmill_exporter_scrape_success == 0` for upstream collection failure, and on
`windmill_health_status{windmill_health_status="unhealthy"} == 1` for Windmill
health. Valid unhealthy HTTP 503 JSON is collected successfully. Failed
collectors omit their data; they do not retain stale values or turn errors into
zero backlog. HTTP 401/403 indicates credentials/role problems, 404 suggests an
unsupported version or incorrect URL prefix, and timeouts may require adjusting
both exporter and Prometheus budgets.

## Development and releases

```sh
go test -race ./...
go vet ./...
go build ./cmd/windmill-exporter
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
golangci-lint fmt --diff
golangci-lint run
govulncheck ./...
zizmor --persona pedantic --strict-collection .github/workflows
```

CI runs on PRs and main pushes. New tags validate the tagged commit, build an
amd64 image without registry credentials, and transfer it to a separate GHCR
publishing job. Tags must be valid Docker tags and point to commits reachable
from `main`; `latest` and `sha-*` are reserved. Stable `vMAJOR.MINOR.PATCH` tags
also update `latest`; other tags do not. A `sha-COMMIT` tag is also published;
deploy by image digest for immutability. No GitHub Release is required.

Repository administrators must configure protected `main`, trusted tag creation,
required Go/image/workflow-security checks, and prohibit tag movement. YAML alone
does not enforce repository rulesets. Actions and the builder image are pinned;
Dependabot proposes updates. Publishing runs no repository code and cannot run
unless release checks, including pedantic zizmor, succeed.