# 0040. The UI backend gets one metric, the controller gets zero, and monitoring stays optional in the chart

Status: Accepted · Date: 2026-10-26 · Design section: Monitoring and registry mirrors

Decided: added exactly one custom metric (`ichor_uiapi_rpc_duration_seconds`, via a single `connectrpc.com/connect` interceptor) because the UI backend exports nothing on its own; added zero custom metrics to the controller because `controller-runtime` already exports reconcile/workqueue health and `kube-state-metrics` already covers cluster-level state (pending approvals, the cluster lock, `TalosOperation` progress) from fields already in CR spec/status; shipped a `ServiceMonitor` for ichor-ui, one `PrometheusRule` with two alerts, and one Grafana dashboard, all as chart options defaulting off, the same opt-in shape `metricsCert.enabled` already established (ADR 0031).

## Context

architecture.md's "Monitoring and registry mirrors" section requires: "The controller
and UI backend export Prometheus metrics, and ServiceMonitors, alert rules
and Grafana dashboards ship with the chart as an option." It also requires
Ichor to "expose the control-plane metrics that Talos binds locally by
default (controller-manager, scheduler, etcd) and restrict scraping to a
CIDR with Talos's ingress firewall" -- that second sentence is Talos machine
config, which belongs in `pkg/render`'s system layer, and is out of scope
here; see "Not covered here" below.

ADR 0031 already settled the controller's metrics *endpoint* and its
optional certificate (`metricsCert.enabled`). What was still open: which
metrics actually need writing, whether the UI backend needs an endpoint at
all, and how many dashboards/alerts are enough.

## Decided: zero new controller metrics

`controller-runtime` v0.24.1 registers eight metrics unconditionally, with
no code required on Ichor's part (`sigs.k8s.io/controller-runtime/pkg/
internal/controller/metrics`): `controller_runtime_reconcile_total` (by `controller`, `result`),
`controller_runtime_reconcile_errors_total`, `controller_runtime_terminal_
reconcile_errors_total`, `controller_runtime_reconcile_panics_total`,
`controller_runtime_reconcile_time_seconds`, `controller_runtime_max_
concurrent_reconciles`, `controller_runtime_active_workers`, and
`controller_runtime_reconcile_timeouts_total`. "The controller exports
Prometheus metrics" is true before Ichor writes a single line.

The real question was which *domain-specific* facts those don't cover.
Five candidates were weighed, and all five turned out to already be
answerable without adding anything to the controller:

- **Reconcile count/latency/error rate per controller.** Exactly
  `controller_runtime_reconcile_total`, `_time_seconds`, `_errors_total`.
  Adding an Ichor-side counter here would just be a second copy of a metric
  that already exists.
- **Workqueue depth / stuck reconciles.** `controller_runtime_active_
  workers` and `_max_concurrent_reconciles` already cover "is this
  controller keeping up." Nothing to add.
- **How many machines are pending approval, right now (a gauge, not a
  counter).** `TalosMachine.spec.approvalState` (`api/v1alpha1/
  talosmachine_types.go`) is a plain enum field already on the object.
  `kube-state-metrics`'s Custom Resource State (CRS) feature reads
  arbitrary JSONPaths -- spec or status -- from any CRD and turns them into
  gauges with no code, only a YAML config the cluster operator supplies to
  their own `kube-state-metrics` deployment. Counting `approvalState:
  pending` is exactly that kind of mapping. Ichor adding its own gauge here
  would duplicate a fact already sitting in the CR, read by a tool this
  project doesn't install (architecture.md: Ichor installs no monitoring
  stack) but that most clusters already run for HPA and other autoscaling.
- **Whether the cluster lock is held, and by whom.** `pkg/clusterlock`
  (`pkg/clusterlock/clusterlock.go`) is, by its own package doc, "a single
  `coordination.k8s.io/v1` Lease per cluster." `kube-state-metrics` ships
  `kube_lease_owner` and `kube_lease_renew_time` for every `Lease` object in
  the cluster *by default* -- no CRS config, no code, nothing Ichor-side at
  all. "Is the lock stale" is `time() - kube_lease_renew_time >
  leaseDurationSeconds`; "who holds it" is `kube_lease_owner`. Already
  fully answered.
- **`TalosOperation` phase and per-machine progress.** `status.phase` and
  `status.machines[].phase` (`api/v1alpha1/talosoperation_types.go`) are
  exactly the shape CRS's `stateSet` and list-valued metric types are built
  for. Same reasoning as the approval-state case: a documented
  `kube-state-metrics` CRS config, not an Ichor-side gauge.

None of this ships from Ichor: architecture.md is explicit that the
monitoring stack itself -- including whatever reads these CR fields -- is
"a documented set of Helm values, or nothing," never something Ichor
installs. A `kube-state-metrics` CRS snippet covering `approvalState` and
`TalosOperation.status` is worth documenting for an operator who already
runs `kube-state-metrics`, but writing and shipping that config is exactly
the kind of optional-component choice architecture.md reserves for the
user, not Ichor. It is not written here, to avoid the same silent-
failure shape ADR 0031 rejected for its bootstrap certificate: a config
file committed into this repo, decoupled from whatever CRS version and
schema the user's actual `kube-state-metrics` deployment runs, would look
authoritative while quietly drifting out of sync with nothing to catch it.

No "build info" gauge (version/commit labels, a common pattern elsewhere)
is added either: it would need a version string threaded through
`ldflags`, a build-tooling change with its own blast radius that the
monitoring options do not take on.

## Decided: one metric for the UI backend, via one interceptor

Unlike the controller, ichor-ui (`cmd/ichor-ui/main.go`) has *no* metrics
endpoint of its own -- it is a plain `net/http` server with no
`controller-runtime` manager underneath it, so there was nothing free here.
architecture.md requires it to export something, and the one domain fact
worth having is Connect RPC latency and outcome, per procedure: is
`ApproveMachine` slow, is `StreamNodeLogs` erroring.

`connectrpc.com/connect` v1.21.0 ships `connect.Interceptor` (`WrapUnary`,
`WrapStreamingClient`, `WrapStreamingHandler`;
`connectrpc.com/connect@v1.21.0/interceptor.go`). That is the mechanism to
use instead of timing code
scattered across `ClusterService`/`MachineService`/`NodeService`/
`PatchService`'s handlers. `internal/uiapi/metrics.go` adds one
`connect.Interceptor` (`MetricsInterceptor`) observing one histogram,
`ichor_uiapi_rpc_duration_seconds{procedure, code}`, registered once per
`*ServiceHandler` constructor via `connect.WithInterceptors` in `cmd/
ichor-ui/main.go`. No separate error-rate counter: the histogram's `code`
label already carries that signal (`code != "ok"`), and a second counter
duplicating part of what `_count` already gives would be exactly the kind
of metric rejected above for the controller.

`/metrics` is served from the same `http.ServeMux`, the same port, and
plain HTTP -- no second listener, no TLS, no `metricsCert`-style opt-in
certificate. ichor-ui is reached only in-cluster today (architecture.md
"Trust": a port-forward, no exposed Service), the same trust boundary the
Connect API itself already sits behind, so a second port with its own
TLS/auth story would protect nothing a scrape of the first port doesn't
already cross. `github.com/prometheus/client_golang` was already an
indirect dependency (pulled in by `controller-runtime`); using it directly
for `promhttp.Handler()` and `promauto` adds no new dependency
(`go.mod`'s diff is a one-line promotion from indirect to direct).

## Decided: one Grafana dashboard, two alerts

**Dashboard: one, not several.** "Ichor overview" (`dist/chart/templates/
grafana/dashboard-configmap.yaml`) carries five panels: reconcile rate and
error rate by controller, reconcile duration p99, and ichor-ui's RPC
latency p99 and error rate by procedure -- everything the metrics above
cover, on one screen. A dashboard is a JSON blob that rots the
moment a panel's metric name or label changes and nothing re-checks it;
splitting one screen's worth of panels into multiple dashboards would only
multiply that liability for no reader benefit. Shipped as a ConfigMap
labeled `grafana_dashboard: "1"`, the sidecar-discovery convention both
`kube-prometheus-stack`'s and Bitnami's Grafana charts support -- inert
unless the cluster's own Grafana runs that sidecar, consistent with Ichor
installing no Grafana itself.

**Alerts: two.** The bar is "would this wake someone up", and only these two pass:

- `IchorControllerDown` (`up{service="ichor-controller-manager-metrics-
  service"} == 0` for 5m, `severity: critical`): the controller is the only
  writer of Talos nodes and the only enforcer of deletion protection
  (architecture.md "Trust"); down means neither is happening.
- `IchorReconcileErrorsPersisting` (`sum by (controller)
  (rate(controller_runtime_reconcile_errors_total[15m])) > 0` for 15m,
  `severity: warning`): a single failed reconcile is retried and is noise;
  a *sustained* nonzero rate over 15 minutes means something the
  controller depends on is persistently broken, not transiently slow.

Both rules are deliberately built only on metrics Ichor itself guarantees
are present (`up` from the controller's own `ServiceMonitor`,
`controller_runtime_reconcile_errors_total` from `controller-runtime`
itself) -- not on `kube_lease_*` or any CRS-derived series from the
previous section's reasoning, since those depend on a `kube-state-metrics`
deployment and config Ichor does not install or guarantee exists. An alert
rule that silently never fires because its metric was never scraped is the
same failure shape ADR 0031 already rejected for a different reason
(the bootstrap-certificate amendment): a mechanism that looks like safety
and is actually silent absence.

## Chart options, all off by default

- `prometheus.enabled` (pre-existing, ADR 0031) now also renders ichor-ui's
  own `ServiceMonitor` (`templates/prometheus/ichor-ui-metrics-monitor.
  yaml`, fixed name `ichor-ui-metrics-monitor`, matching `ichor-ui-service`'s
  own fixed-name reasoning) alongside the controller's.
- `prometheus.rules.enabled` (new): the `PrometheusRule`
  (`templates/prometheus/alert-rules.yaml`), independent of
  `prometheus.enabled` -- a user might want the `ServiceMonitor` without
  opting into alerting, or vice versa once `prometheus-operator`'s
  `PrometheusRule` CRD exists.
- `grafana.dashboards.enabled` (new): the dashboard ConfigMap
  (`templates/grafana/dashboard-configmap.yaml`).

All three default `false`, the same shape `metricsCert.enabled` already
uses (ADR 0031): rendering nothing is the default, each flag renders
exactly its own resource. With all three on, the UI `ServiceMonitor` exists,
both named alerts are in the rendered `PrometheusRule`, and the dashboard
ConfigMap's `ichor-overview.json` value is valid JSON with at least one panel;
`dist/chart/chart_test.go` pins this down.

`config/prometheus/` (the kustomize mirror, CONTRIBUTING.md's "mirroring
the matching `config/` base when one exists") gained `ui-monitor.yaml` and
`alert-rules.yaml`, bundled behind the existing single `[PROMETHEUS]`
toggle in `config/default/kustomization.yaml` -- kustomize's coarser
one-switch convention doesn't need the chart's finer
`prometheus.enabled`/`prometheus.rules.enabled` split to stay useful for
local dev. The Grafana dashboard has no `config/` mirror: there is no `config/
grafana/` directory, nothing else in `config/`
wires up Grafana's sidecar-discovery convention, and kustomize's audience
(contributors rendering `config/default` for local iteration) has no
Grafana to discover it. The kustomize resources render as `ichor-alert-rules`
and `ichor-ichor-ui-metrics-monitor`; the double `ichor-ichor-` prefix on the
UI one is the existing kustomize naming convention every other
`config/manager-ui` resource already has, not a defect.

## Not covered here: Talos control-plane metrics

architecture.md's "expose the control-plane metrics that Talos binds
locally by default (controller-manager, scheduler, etcd) and restrict
scraping to a CIDR with Talos's ingress firewall" is Talos machine
configuration -- a `machine.controlPlane`-level binding (or equivalent) plus
`machine.network.kubespan`/ingress-firewall-style CIDR rules -- that belongs
in `pkg/render`'s system layer, not in the chart or `internal/uiapi`, so
this ADR does not implement it (ADR 0069 does). What that work has to
decide and build:

- Which of `controller-manager`/`scheduler`/`etcd`'s locally-bound metrics
  ports get exposed, and on which typed `TalosConfigPatch` field(s)
  (ADR 0003's typed-mirror types, not raw YAML -- CLAUDE.md's "Machine
  config is typed only").
- The ingress-firewall CIDR restriction that scopes who can reach those
  ports once exposed -- without it, "expose the metrics" and "open the
  control plane to the whole pod network" are the same change.
- Whether exposing these needs its own chart/config option (`prometheus.enabled` only covers the controller and ichor-ui's own
  `/metrics`, served from this Kubernetes cluster, not a Talos node's).

## Consequences

- `internal/uiapi/metrics.go` adds one histogram (`ichor_uiapi_rpc_
  duration_seconds`) and one `connect.Interceptor`
  (`MetricsInterceptor`), wired into every `*ServiceHandler` constructor in
  `cmd/ichor-ui/main.go` via `connect.WithInterceptors`.
- `cmd/ichor-ui/main.go` serves `/metrics` (`promhttp.Handler()`) on its
  existing mux and port -- no new listener, no new chart Service port.
- The controller (`internal/controller/`, `cmd/main.go`'s manager wiring)
  is untouched: zero new metrics, per the reasoning above. Anyone wanting
  cluster-state gauges (pending approvals, lock status, `TalosOperation`
  progress) points `kube-state-metrics`'s CRS config at the CR fields and
  `Lease` object that already carry them.
- `dist/chart/templates/prometheus/ichor-ui-metrics-monitor.yaml`,
  `alert-rules.yaml`, and `dist/chart/templates/grafana/
  dashboard-configmap.yaml` are new, each gated by its own `values.yaml`
  flag (`prometheus.enabled`, `prometheus.rules.enabled`,
  `grafana.dashboards.enabled`), all defaulting `false`.
- `dist/chart/templates/ui/ui-service.yaml` and `config/manager-ui/
  service.yaml` both name their port `http` so the new `ServiceMonitor`s
  can target it by name, mirroring `metrics/controller-manager-metrics-
  service.yaml`'s own named port.
- `config/prometheus/` gained `ui-monitor.yaml` and `alert-rules.yaml`,
  wired into the existing `[PROMETHEUS]` toggle; no `config/grafana/`
  directory was created (reasoning above).
- `go.mod`: `github.com/prometheus/client_golang` moved from an indirect to
  a direct dependency; no new dependency was added.
- Update: the Talos control-plane metrics binding and the ingress-firewall CIDR restriction were built in ADR 0069 (`spec.controlPlaneMetrics`).
