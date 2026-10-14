# Kubernetes

[../deploy/kubernetes.yaml](../deploy/kubernetes.yaml) is a small Deployment,
ClusterIP Service and dedicated ServiceAccount example. It uses the default
collector list and needs no Helm chart, CRDs, RBAC permissions or shell in the image.

Before applying it:

- Replace `VERSION` with a published version or, preferably, an immutable image digest.
- Set `WINDMILL_URL` to the actual internal Service address/port or HTTPS URL.
- Supply the `windmill-exporter-token` Secret with a `token` key using your approved
  secret manager. The value must not contain whitespace or a trailing newline.
  Never commit token values or Secret manifests to git.
- Add the opt-in collector/workspace environment settings needed for your instance.
- Set the namespace when applying, and configure registry access if GHCR is private.

```sh
kubectl -n YOUR_NAMESPACE apply -f deploy/kubernetes.yaml
```

## Probes

| Endpoint | Kubernetes Probe | Healthy When |
| --- | --- | --- |
| `/startupz` | Startup | Configuration, client, routes and listener have initialized |
| `/readyz` | Readiness | Startup is complete and the exporter is not draining |
| `/livez` | Liveness | The exporter HTTP handler is responding |

Healthy probes return HTTP 200; startup/readiness return 503 when their conditions
are not met. `/healthz` remains a liveness alias for existing deployments.
All probes are cheap, uncached and independent of Windmill/API credentials/network
availability. A Windmill outage must not cause exporter restart loops or remove
the exporter from monitoring. Alert on collector/scrape metrics for upstream faults.

## Termination

SIGTERM marks the exporter unready and rejects new `/metrics` requests with 503.
It waits `EXPORTER_DRAIN_DELAY` (default 5s) for readiness/endpoint propagation,
then drains accepted HTTP requests. Active scrapes retain their request context
until completion or the total shutdown deadline. A second signal forces normal
process termination.

`EXPORTER_SHUTDOWN_TIMEOUT` includes the drain delay and defaults to scrape timeout
plus drain delay plus 5s (20s with defaults). It must exceed scrape timeout plus
drain delay. Keep `terminationGracePeriodSeconds` greater than that total, with
headroom for kubelet/runtime work; the example uses 30s. Increase it when increasing
the exporter timeout. Do not add an exec-based sleep/curl preStop hook: the image
has no shell, and the application already performs its drain delay.

## Runtime Settings

The example runs as UID/GID 65532 with a read-only root filesystem, dropped
capabilities, no privilege escalation and RuntimeDefault seccomp. No Kubernetes
API access is needed, so service-account token mounting is disabled. Node selectors
ensure the linux/amd64 image is scheduled on compatible nodes.

Requests/limits are starting values, not capacity guarantees. Tune them against
workspace count, worker inventory and scrape timings. The 96MiB `GOMEMLIMIT` leaves
headroom under the 128Mi container limit; it is a soft Go heap/runtime target, not
a hard process-memory cap. Adjust both together. Go uses container-aware CPU defaults.
Pod UID supplies `service.instance.id`; namespace supplies `service.namespace`.
Environment Secret changes require a rollout to restart existing Pods.

Keep the Service private. Use NetworkPolicies to permit your Prometheus clients
and kubelet probes, plus required DNS/Windmill egress; avoid blindly copying policy
selectors from another cluster. Prefer HTTPS and mount private CA certificates
through `SSL_CERT_FILE` when necessary. Configure your Prometheus discovery or a
ServiceMonitor/PodMonitor separately; this example does not install an operator.
Start with one replica to avoid duplicate upstream collection. For HA replicas,
retain instance labels and avoid summing duplicate Windmill snapshot gauges.