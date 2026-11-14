# 0031. Fixing two kubebuilder scaffold remnants that actually ship

Status: Accepted, amended twice · Date: 2026-10-26 · Design section: 4.4 (Typed configuration), Monitoring

Decided: made `config/samples/*.yaml` minimal and valid instead of deleting them; gave the metrics endpoint a real, opt-in certificate named `metricsCert.enabled` (not `certManager`, and not the default) via a second cert-controller rotator instead of leaving `insecureSkipVerify`'s documented escape hatch wired to nothing; noted that the webhook certificate has the same cold-start race the metrics one does; and logged that race instead of papering over it with a bootstrap certificate, which would trade a loud failure for a silent one.

## Context

Two kubebuilder-scaffolded remnants, both still exactly as kubebuilder wrote
them: `config/samples/*.yaml` (five files,
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
samples/` falls behind, rather than silently.

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
the toggle, and the ServiceMonitor's own TLS block
referenced the unprefixed `metrics-server-cert` while the fixed name
`cmd/main.go` would need to use is `ichor-metrics-server-cert`, the same
`ichor-` prefix every other fixed-name resource in this project already
carries.

**`cert-controller` can rotate a second certificate.** It already runs
in-process for the webhook certificate (ADR 0006), so a second instance is
the smallest possible fix: no new dependency, no new mechanism.
`pkg/rotator.CertRotator.ControllerName`'s own doc
comment in the vendored source says so: "allows registering multiple
cert-rotator controllers... unless rotating multiple certificate secrets."
Reading `AddRotator` confirms why: every field it needs (`reader`, `writer`,
the readiness channels) lives on the `CertRotator` value itself, not in
package-level state, so two instances don't interfere — the only thing that
collides if left at its default is `ControllerName`, since
`addController(mgr, reconciler, cr.ControllerName)` registers each
controller on the manager under that name.

The second known gotcha ADR 0006 already recorded for the webhook
certificate — `cert-controller` v0.16.0 has no `Create` path, only `Update`,
as `pkg/rotator.writeSecret` shows — applies
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

**The cold-start race.** `cmd/main.go`'s comment on the webhook rotator
used to claim "the webhook
handlers are registered only once the rotator reports the certificate is
in place, so the webhook server never starts serving with nothing to
present." Against `sigs.k8s.io/controller-runtime@v0.24.1`'s actual
manager internals, that is wrong about the mechanism, though not
about the outcome:

- `(*controllerManager).Start` (`pkg/manager/internal.go`) runs
  `runnables.HTTPServers.Start` (the metrics server's group) and then
  `runnables.Webhooks.Start` (the webhook server's group) *before*
  `runnables.Caches.Start`, `runnables.Others.Start`, or the leader-election
  section that is where a `RequireLeaderElection: true` `CertRotator`
  actually runs.
- Both of those `.Start` calls, however, only block until each runnable
  signals *ready* (`pkg/manager/runnable_group.go`'s `readyRunnable.Check`,
  which a plain `manager.Runnable` without a `Checker` satisfies
  immediately) — not until the runnable's own `Start` has run to
  completion or failed. The actual `Start` call runs in a separate
  goroutine; an error from it goes to `errChan`, which the outermost
  `select` at the end of `(*controllerManager).Start` is waiting on.
- `webhook.DefaultServer.Start` (`pkg/webhook/server.go`) and
  `metricsserver.(*defaultServer).createListener`
  (`pkg/metrics/server/server.go`) both read the certificate from
  `CertDir` exactly once, synchronously, with no retry: the webhook server
  via `certwatcher.New`, which does an initial `ReadCertificate` and
  returns an error if it fails; the metrics server via a one-time
  `os.Stat` that silently falls back to an ephemeral self-signed
  certificate forever (for that process) if the file isn't there *yet* --
  not even an error, just permanently stuck on self-signed until a
  restart.

Put together: on a cold start (Secret shipped empty, no `data` yet,
exactly `config/webhook/secret.yaml`'s and
`metrics_server_cert_secret.yaml`'s own shape), the webhook server's
`Start` races the `CertRotator`'s `Start` to be the first to touch the
cert file — the deferred `SetupTalosClusterWebhookWithManager` call
changes *when admission logic starts enforcing*, not whether the
listener can come up without a cert. **The webhook certificate has this
race**, contrary to its former inline comment, which now states the actual mechanism.
**The race is not new**: ADR 0006's own "Consequences" section already documents and
accepts it, framed around the Secret being *deleted* ("the pod
crash-loops... That is the intended direction of failure") — the same
mechanism covers the Secret existing-but-empty case, it just isn't named
there explicitly. `failurePolicy: Fail`
(`config/webhook/manifests.yaml`) means every write matching the webhook's
rules is refused for as long as the race keeps losing, which is squarely
what ADR 0006 already decided to accept rather than fail open.

**The metrics certificate's failure mode is worse than "a scrape fails
visibly"**: the metrics server is
a `manager.Runnable` through the identical `HTTPServers` group, so losing
its own cold-start race doesn't just leave Prometheus unable to verify a
certificate -- it fails `mgr.Start()` itself and crash-loops the *entire*
controller, deletion protection included, the same as a lost webhook race
would. This is the reason the amendment below keeps
`metricsCert.enabled` off by default rather than flipping it to always-on.

**The name `metricsCert`, not `certManager`**: the chart's toggle was first
called `certManager.enabled`. Anyone reading `certManager: enabled: true`
reaches for cert-manager, which plays no part in this at all:
`open-policy-agent/cert-controller` rotates its own certificate in-process.
The name was actively misleading, and no release had shipped the old name,
so renaming cost nothing. The toggle is `metricsCert.enabled`
(`dist/chart/values.yaml`, `templates/manager/manager.yaml`,
`templates/extras/metrics-server-cert.yaml`,
`templates/prometheus/controller-manager-metrics-monitor.yaml`), and
`values.yaml`'s comment states plainly why this isn't cert-manager.

Both the kustomize opt-in chain (`../prometheus`, `metrics_server_cert_
secret.yaml`, `cert_metrics_manager_patch.yaml`, `monitor_tls_patch.yaml`)
and the chart's (`metricsCert.enabled`, `prometheus.enabled`) stay off by
default. Opting in produces a working TLS setup instead of a
rejected admission request or (for the chart) a `ServiceMonitor` quietly
referencing a `Secret` that was never created: the Secret, the Deployment's
volume and argument, and the three places the `ServiceMonitor` names the
Secret must all agree, and `dist/chart/chart_test.go`'s
`TestMetricsCertEnabled` pins that down. Why the default stays off is
answered in the amendment below.

## Amendment (same date): why `metricsCert.enabled` stays off by default

Since `cert-controller` is already running in-process
unconditionally for the webhook certificate, could the second rotator run
unconditionally too, making a real metrics certificate the default instead
of an opt-in? The answer would differ if metrics scraping itself were off,
but it isn't.
`dist/chart/values.yaml`'s `metrics.enabled` defaults to `true`, and
`config/default/kustomization.yaml`'s `manager_metrics_patch.yaml` patch
(which sets `--metrics-bind-address=:8443`) is *not* one of the commented,
opt-in lines — it is active in a plain `kustomize build config/default`
today. The metrics endpoint, serving HTTPS with controller-runtime's own
ephemeral self-signed certificate, is already live by default in both the
chart and kustomize; a real certificate would not be provisioned for a
feature nobody turned on.

That rules out "wasted on a disabled feature" as the reason to stay
opt-in. The reason is the race described above: defaulting
`--metrics-cert-path` to non-empty makes the metrics server's own
`Runnable` start checking for a cert file that, on a cold cluster, the
rotator has not written yet — and because that `Runnable` sits in the same
`HTTPServers` group and the same `mgr.Start()` error path as the webhook
server, losing that race does not degrade gracefully to "self-signed for
now" or "scraping fails until it catches up." It fails `mgr.Start()` and
crash-loops the *entire* controller, deletion protection included, on
every fresh install, for a certificate nothing currently verifies by
default (`prometheus.enabled` stays off, and `metricsCert.enabled`
defaulting on would be the only consumer of the certificate it provisions
until someone also turns that on). Weighed against each other: the
webhook's identical crash-loop is accepted because failing closed on
deletion protection is a deliberate security property (ADR 0006: "a
deletion guard that opens when the controller is down is not a guard");
a metrics endpoint's TLS verifiability has no equivalent claim on being
worth crash-looping the whole controller for, by default, on every
install. One Secret and one volume mount being "always present" was
considered and is not the blocker — both are cheap, and the webhook certificate
already has the same shape.

**Decision: stays opt-in.** Revisit if cert-controller ever gains the
ability to signal "not ready yet" to the metrics server's own listener
construction (it does not today: a one-time `os.Stat`
with no retry and no readiness hook), which would let a cold start wait
instead of crash-loop.

## Amendment (same date): the cold-start race is logged, not fixed

Considered actually closing the race rather than only confirming where it
lives: have `cmd/main.go` write a throwaway, self-signed bootstrap
certificate straight into `CertDir` before `mgr.Start()`, so the webhook
and metrics listeners always find *something* on disk, real or not.
**Rejected.** Two reasons, weighed against the status quo:

- The crash-loop already self-heals. Kubernetes retries a failing
  container with backoff, and each fresh container mount reads the
  Secret's then-current content from scratch (no stale-volume wait the
  way an *already-mounted* Secret picking up a *later* change would need)
  -- this is expected to converge within the first one or two restarts,
  well under a minute, once cert-controller's own write
  succeeds before the listener's next attempt.
- A bootstrap certificate trades a loud, short-lived failure for a silent
  one that can last forever. If cert-controller's rotator ever can't
  write the real Secret -- RBAC drift, a quota, anything -- the listener
  would keep serving the self-signed bootstrap certificate indefinitely,
  nothing would be visibly wrong (no crash, no restart, healthy-looking
  probes), and `failurePolicy: Fail` would keep rejecting every DELETE
  against a certificate nobody actually trusts, with no signal pointing
  at why. A new code path on the single most security-sensitive part of
  startup, to buy a failure mode strictly worse than the one already
  accepted (ADR 0006's "Consequences": crash-loop is the intended
  direction).

**Decision: log it instead.** `cmd/main.go`'s `logCertStatusBeforeStart`
checks, once, right before `mgr.Start()`, whether each enabled server's
cert file already exists, and if not, logs the exact mechanism
(not a guess) in one line: that this is expected on a brand-new install,
and that it resolves on its own once the rotator's write reaches the
Secret and the mounted volume. The chart's `values.yaml` carries the same
note next to `webhook.enabled`, for whoever reads values before looking at
pod logs. The message states both halves: expected on a fresh install, and what
resolves it.

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
- The chart's `metricsCert.enabled: true` now actually provisions a
  certificate (`extras/metrics-server-cert.yaml`, gated `manager.yaml`
  volume/mount/arg) instead of only toggling the `ServiceMonitor`'s TLS
  block against a `Secret` that was never created.
- The chart's toggle is `metricsCert.enabled`, not `certManager.enabled`;
  no release ever shipped the old name,
  so no migration note is needed.
- `cmd/main.go`'s webhook-rotator comment no longer claims handler
  registration prevents the webhook server's listener from racing the
  rotator's first write; it states the actual mechanism and
  points at ADR 0006's already-accepted crash-loop consequence instead.
- `cmd/main.go`'s `logCertStatusBeforeStart` logs, once per enabled server,
  right before `mgr.Start()`, whether that server's certificate file
  already exists -- and if not, states plainly that a restart or two is
  expected on a brand-new install and names what resolves it. `dist/
  chart/values.yaml` carries the same note next to `webhook.enabled`.
  The race itself is not fixed: a bootstrap self-signed certificate was
  considered and rejected, since it would trade this loud, self-healing
  failure for a silent one that can reject every DELETE forever if the
  rotator's own write ever fails for an unrelated reason.
