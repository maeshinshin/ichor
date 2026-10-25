# 0031. Fixing two kubebuilder scaffold remnants that actually ship

Status: Accepted · Date: 2026-10-26 · Design section: 4.4 (Typed configuration), Monitoring

Decided: made `config/samples/*.yaml` minimal, valid and apply-tested instead of deleting them, and gave the metrics endpoint a real certificate via a second cert-controller rotator instead of leaving `insecureSkipVerify`'s documented escape hatch wired to nothing.

## Context

Two kubebuilder-scaffolded remnants, both still exactly as kubebuilder wrote
them, were found during a review pass: `config/samples/*.yaml` (five files,
every one still `# TODO(user): Add fields here`) and
`config/prometheus/monitor.yaml` (`insecureSkipVerify: true`, with a comment
pointing at an opt-in TLS patch). Both ship — `config/samples/` is read by
anyone looking for a usage example, and `config/prometheus/monitor.yaml` is
what a Prometheus-scraping deployment actually gets — so both needed a
decision, not a note.

## Decision: config/samples -- minimal and valid, not deleted

Deleting the five samples was the other option and was rejected.
`TalosConfigPatch.spec.documents[]` is the one API in this project hardest
to guess the shape of by reading the CRD schema alone (ADR 0003's typed
mirror types have no raw-YAML form to fall back on), so one working example
is worth more than removing scaffold debris. The actual defect was narrower
than "the samples are placeholders": every one of the five kinds has a
required field (`TalosCluster.spec.endpoint`/`secretsRef`/`controlPlane`,
`TalosMachine.spec.identity`/`endpoint`, `TalosConfigPatch.spec.layer`,
`TalosOperation.spec.kind`, `TalosStorage.spec.diskPreset`/`diskSelector`),
so `# TODO(user): Add fields here` doesn't just fail to demonstrate anything
— it fails to apply at all. The first person who copies one gets a rejected
API request and no reason to suspect the sample itself.

Each of the five is now a minimal, valid object: required fields filled
with realistic placeholder values (an IPv4 VIP, semver versions, a sample
UUID), nothing exercising a CEL validation rule on purpose — that is
`validation_test.go`'s job, not a sample's, and a sample that happened to
trip one by accident would be exactly as confusing as the TODO it replaces.
`TalosConfigPatch`'s sample carries one `documents[]` entry (a `SysctlConfig`
setting `net.core.somaxconn`), chosen as the simplest of the six allowed
kinds that still shows the discriminated-union shape (`kind` plus the one
matching field) a reader would otherwise have to reconstruct from
`talosconfigpatch_types.go` and `pkg/typedconfig` by hand.

`api/v1alpha1/samples_test.go` is what keeps this from rotting the way the
original scaffold did: one Ginkgo `It` per sample file, each actually
`Create`d against the suite's own envtest apiserver (the same one
`validation_test.go` already runs against), so a later required-field
addition to any of the five kinds fails this suite the moment `config/
samples/` falls behind, rather than silently. Verified empirically, not
just by inspection: reverting one sample back to the TODO placeholder and
running the suite failed exactly that spec, naming the file, while the
other four passed.

## Decision: the metrics endpoint gets a real certificate, via cert-controller's second rotator

`config/prometheus/monitor.yaml`'s `insecureSkipVerify: true` is the correct
default — out of the box the metrics server presents controller-runtime's
own ephemeral self-signed certificate, which Prometheus has no CA to verify
against, so skipping verification is the only way scraping would work at
all. The comment pointing at `config/prometheus/monitor_tls_patch.yaml` as
the production alternative was the actual defect: that patch referenced
`SERVICE_NAME.SERVICE_NAMESPACE.svc`, literal text with no kustomize
`replacements:` block anywhere to substitute it, and no `metrics-server-cert`
Secret existed for it to reference even if the name resolved. Applying the
documented fix would not have worked. The Helm chart's `certManager.enabled`
toggle (despite its name, not an actual cert-manager integration — see
below) had the identical defect one layer worse: nothing in `manager.yaml`
mounted a metrics-cert volume or passed `--metrics-cert-path` regardless of
the toggle, and — found only by actually rendering the chart with the flag
on, not by reading the templates — the ServiceMonitor's own TLS block
referenced the unprefixed `metrics-server-cert` while the fixed name
`cmd/main.go` would need to use is `ichor-metrics-server-cert`, the same
`ichor-` prefix every other fixed-name resource in this project already
carries.

**Investigated before building anything**: whether `cert-controller`
(already running in-process for the webhook certificate, ADR 0006) can
rotate a second certificate, since that is the smallest possible fix if it
can — no new dependency, no new mechanism, just a second instance of one
already depended on. `pkg/rotator.CertRotator.ControllerName`'s own doc
comment in the vendored source settles it: "allows registering multiple
cert-rotator controllers... unless rotating multiple certificate secrets."
Reading `AddRotator` confirms why: every field it needs (`reader`, `writer`,
the readiness channels) lives on the `CertRotator` value itself, not in
package-level state, so two instances don't interfere — the only thing that
collides if left at its default is `ControllerName`, since
`addController(mgr, reconciler, cr.ControllerName)` registers each
controller on the manager under that name.

The second known gotcha ADR 0006 already recorded for the webhook
certificate — `cert-controller` v0.16.0 has no `Create` path, only `Update`,
confirmed again here by reading `pkg/rotator.writeSecret` itself — applies
identically to a second certificate: its Secret must already exist, empty,
before the rotator's first reconcile. `config/default/
metrics_server_cert_secret.yaml` and the chart's `extras/
metrics-server-cert.yaml` both ship it that way, the same pattern
`config/webhook/secret.yaml` and `extras/webhook-server-cert.yaml` already
established for the webhook certificate.

`cmd/main.go` gains a second `rotator.AddRotator` call, gated on
`metricsCertPath` being set — the same signal `metricsServerOptions.CertDir`
a few lines above it already uses to mean "a real certificate was
requested" — with `ControllerName: "ichor-metrics-cert-rotator"` so it
doesn't collide with the webhook rotator's default name, and `DNSName`
matching the metrics Service (`ichor-controller-manager-metrics-service.
<namespace>.svc`) the same way the webhook rotator's `DNSName` matches the
webhook Service. `RequireLeaderElection: true`, matching the webhook
rotator's own reasoning (ADR 0006's amendment, principle 6): two replicas
racing to generate a CA on a cold start is a problem regardless of which
Secret is being written.

**Not solved, inherited as-is**: both the webhook and metrics TLS servers
read their certificate from disk once, at listener-creation time
(`certwatcher.New` for the webhook server, an `os.Stat` check for the
metrics server — confirmed by reading both in `sigs.k8s.io/
controller-runtime@v0.24.1`), and the rotator populates the Secret
asynchronously as its own manager `Runnable`. A cold start where the
listener is created before the Secret volume is first populated is a
latent race in cert-controller's whole model, not something this unit
introduces — the metrics rotator inherits exactly the characteristics the
already-shipped webhook rotator has, nothing better and nothing worse.
Revisit only if it is ever observed to actually bite in practice; nothing
here changes that calculus for the webhook certificate, which this unit
does not touch.

**On the name "certManager"**: the chart's `certManager.enabled` toggle
predates this unit and is not renamed here — renaming a published Helm
value is a breaking change to the chart's interface, out of proportion to
a documentation/wiring fix. Its own `values.yaml` comment is rewritten to
say plainly that it is not an external cert-manager integration; Ichor
ships no certificate-issuing dependency either way (ADR 0006).

Both the kustomize opt-in chain (`../prometheus`, `metrics_server_cert_
secret.yaml`, `cert_metrics_manager_patch.yaml`, `monitor_tls_patch.yaml`)
and the chart's (`certManager.enabled`, `prometheus.enabled`) stay off by
default, unchanged from before this unit — the decision here is that
opting in now produces a working, verifiable TLS setup instead of a
rejected admission request or (for the chart) a `ServiceMonitor` quietly
referencing a `Secret` that was never created. Verified for kustomize by
temporarily uncommenting the full opt-in chain and running `make
build-installer`, confirming the rendered Secret, Deployment volume/arg and
`ServiceMonitor` TLS block all agree, then reverting; verified for the
chart by `helm template --set certManager.enabled=true --set
prometheus.enabled=true` and a new test,
`dist/chart/chart_test.go`'s `TestMetricsCertManagerEnabled`, which renders
with the flag on and asserts the Secret and the three places the
`ServiceMonitor` names it all agree — exactly the mismatch this unit found
and fixed, now guarded against regressing silently.

## Consequences

- `config/samples/*.yaml` apply as committed; `api/v1alpha1/samples_test.go`
  fails naming the exact file if any of the five ever stops applying.
- `config/prometheus/monitor.yaml` still ships `insecureSkipVerify: true`
  by default, now with an opt-in path (`monitor_tls_patch.yaml` plus
  `[METRICS-WITH-CERTS]` in `config/default/kustomization.yaml`) that
  actually works when followed, instead of one that looked like it would
  and didn't.
- `cmd/main.go` runs two independent `cert-controller` rotators when both
  are enabled (webhook always, metrics opt-in), each with its own
  `ControllerName`, `SecretKey` and `CertDir`; neither is aware of the
  other.
- The chart's `certManager.enabled: true` now actually provisions a
  certificate (`extras/metrics-server-cert.yaml`, gated `manager.yaml`
  volume/mount/arg) instead of only toggling the `ServiceMonitor`'s TLS
  block against a `Secret` that was never created.
- `internal/controller/`, `internal/cli/`, `internal/uiapi/`, `pkg/`,
  `test/`, `ui/` and `proto/` are untouched by this unit.
