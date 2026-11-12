# 0006. Webhook serving certificates without cert-manager, failing closed

Status: Accepted · Date: 2026-10-25 · Design section: 4.5, 5.2.7

Decided: Run `cert-controller` in-process for the webhook serving certificate instead of depending on cert-manager

## Context

Ichor needs validating webhooks from phase 1 on — deletion protection first,
then topology and immutability checks. Two project rules bear on how their
serving certificates work: the default install must not require cert-manager,
and nothing in the chart may produce a diff on every Argo CD sync. kubebuilder
4.15.0 scaffolds a `config/certmanager/` Issuer and assumes cert-manager at
runtime; it offers no alternative.

Constraints from the dependencies:

- controller-runtime v0.24.1 only *reloads* certificates. `certwatcher` watches
  files and `webhook.Server` points at `CertDir`; nothing in it generates a CA
  or a serving certificate.
- `caBundle` cannot be avoided. It is optional in the API, but when it is
  absent the API server falls back to its own system trust roots — not the
  cluster CA. None of the built-in CSR signers in 1.36
  (`kube-apiserver-client`, `kube-apiserver-client-kubelet`,
  `kubelet-serving`, `legacy-unknown`) issues a serving certificate the API
  server will trust for an admission webhook. So for a self-signed setup some
  component has to write `caBundle` at runtime.
- `github.com/open-policy-agent/cert-controller` (Apache-2.0) does exactly
  that job as a library: generates a self-signed CA and serving certificate
  into a Secret, rotates ahead of expiry, and injects `caBundle` into
  validating and mutating webhook configurations, CRD conversion webhooks and
  APIServices. It builds against controller-runtime v0.24.1. Its last feature
  release is v0.16.0 (March 2025); since then it is dependency bumps only.
- Gatekeeper's chart — cert-controller's own reference consumer — ships the
  webhook configuration with no `caBundle` key, and `caBundle` is written at
  runtime. It does, however, ship the certificate **Secret**, as metadata with
  no `data`, and that is load-bearing: cert-controller v0.16.0
  contains no `Create` call anywhere. `refreshCertIfNeeded` does
  `reader.Get` on the Secret and returns a wrapped error if that fails, so on
  a cluster where the Secret does not exist the rotator errors out, the manager
  runnable fails and the process exits — on every restart. The empty Secret is
  what makes `Get` succeed; `secret.Data == nil` is then what triggers
  generation, and `Update` fills it in.
- On the Argo CD side, `ignoreDifferences` alone only hides the diff; self-heal
  still overwrites the field unless the Application also sets
  `RespectIgnoreDifferences=true`. Keying the exception on
  `managedFieldsManagers` requires the writer to set a field manager name.

## Decision

- Run `open-policy-agent/cert-controller` in-process in `ichor-controller`,
  with `CertRotator.FieldOwner` set to `ichor-controller`, the field manager
  the naming conventions already assign to it. Serving certificates are read
  from the mounted Secret by controller-runtime's `certwatcher`, so the two
  compose and Ichor owns no cryptography.
- The chart and the Kustomize base ship the `ValidatingWebhookConfiguration`
  **without a `caBundle` key**, and ship the certificate Secret as **metadata
  only, with no `data`**. No key material is templated, so no generated value
  can differ on a re-sync, but the object exists — which is what lets the
  rotator's `Get` succeed and its `Update` fill it in. Shipping it in Git has a
  second benefit: if the Secret is ever deleted, Argo CD puts the empty one
  back and the controller refills it, instead of the operator having to.
- The volume that mounts it is `optional: true` as well. With the Secret
  shipped this is belt and braces, but it changes the failure mode if the
  Secret goes missing: the pod starts and crash-loops with the reason in its
  logs, rather than sitting in `FailedMount` where the cause is far less
  visible.
- **`failurePolicy: Fail`**, which is where Ichor departs from Gatekeeper's
  `Ignore`, deliberately. Gatekeeper validates the whole cluster, so a webhook
  that is down must not block everything. Ichor's webhooks match only
  `ichor.maesh.dev` resources, so failing closed blocks Ichor operations and
  nothing else — and the thing they mostly protect is deletion of
  `TalosCluster` and `TalosMachine`, where failing open lets an Argo CD prune
  cascade into node resets. A deletion guard that opens when the controller is
  down is not a guard.
- Set `RequireLeaderElection` on the rotator. The controller runs two
  replicas, and without it both run the rotator: on a cold start both can see
  no Secret and generate a different CA each. `FieldOwner` only decides who
  writes last, so the losing replica would serve a certificate signed by a CA
  the live `caBundle` does not name until the mounted Secret resyncs. Letting
  only the leader rotate is the same instinct as principle 6 — one writer for
  a disruptive operation — and costs nothing, since both replicas need the
  Secret before they can serve anyway.
- Ship, with the chart, an `ignoreDifferences` entry for the webhook
  configuration's `caBundle` and one for the certificate Secret, both keyed on
  `managedFieldsManagers: [ichor-controller]`, together with the
  `RespectIgnoreDifferences=true` sync option, and say in the docs that both
  are needed — the first one alone leaves self-heal to clobber the field.
  Example: `dist/chart/examples/argocd-application.yaml`.
- cert-manager stays available as an opt-in: a chart value turns the in-process
  rotator off. The design requires only that it not be the default.

## Consequences

- Until the controller is running and has written `caBundle`, writes to Ichor's
  CRs are refused. That is the right order regardless — the sync waves already
  place Ichor's CRDs and controller ahead of Ichor's CRs — but `ichor init`
  has to wait for the webhook to answer before it creates the first CRs, and
  the bootstrap path must treat "webhook not ready yet" as something to wait
  on rather than an error.
- If the Secret is deleted while the webhook configuration still carries the
  old `caBundle`, the rotator's `Get` fails, `Start` returns an error and the
  manager exits, so the pod crash-loops rather than serving with a stale
  certificate. Under Argo CD the empty Secret comes back on the next sync and
  the controller refills it. With `failurePolicy: Fail` that means Ichor operations
  stop until it recovers. That is the intended direction of failure, and
  `ichor` CLI recovery paths must not depend on the webhook.
- Neither library exposes certificate expiry as a metric or a condition.
  Ichor's own expiry gauge lands with the rest of the metrics in phase 9; until
  then expiry is visible only in logs. Recorded as a known gap, not a surprise.
- cert-controller is maintained but not developed. The surface Ichor uses is one
  function and one struct, so if it stops building against a future
  controller-runtime, replacing it with an in-house rotator is a contained
  change rather than a rewrite.

## Alternatives considered

**cert-manager as the default.** Ruled out by the design, which wants the
default install to carry no such dependency.

**Our own rotator.** Roughly 200 lines of certificate generation, rotation
windows and failure handling that cert-controller already has, in exchange for
one fewer dependency and metrics from day one. Kept as the fallback.

**A Helm-hook Job (`kube-webhook-certgen`-style).** Well established, used by
ingress-nginx, and needs no Go code from us — but it is a point-in-time Job
with no continuous rotation, and it adds hook-ordering bookkeeping under Argo
CD that the in-process rotator does not need.

## Amendment: cold-start ordering, no crash restarts

A fresh install must converge on its own. The rotator is leader-only (see
Decision), but controller-runtime's webhook server starts at once, reads
`tls.crt`, finds nothing and stops the manager as soon as the lease is acquired,
before the rotator has written anything. With the 120 s lease and no release on
cancel (ADR 0063, ADR 0089), every dead holder then blocks the next container
for up to 120 s and the pod backoff grows to 5 min. The "one or two expected
restarts" of ADR 0031 holds only while the lease is short.

Decision: the webhook server does not start until its certificate exists.

- controller-runtime v0.24.1 adds the webhook server to the manager lazily, on the
  first `mgr.GetWebhookServer()` (`webhookServerOnce`). So that call, every
  `Setup*WebhookWithManager` and the readiness check all move behind one gate
  (`webhooksUp` in `cmd/main.go`), in a goroutine.
- The gate opens on either trigger: the rotator closes `IsReady` (the leader), or
  `tls.crt` and `tls.key` exist and are non-empty in the cert directory (polled
  every second, `waitForCertFiles`). The second is how a non-leader replica, which
  runs no rotator, serves once the leader has written the Secret and the kubelet
  has synced the volume. Either replica becomes Ready on its own.
- Readiness stays false until the gate is open (`gatedChecker`) and then delegates
  to the server's `StartedChecker`, so Ready still means "the webhook answers".
- Only the leader writes the certificate, so a single writer is kept and the
  cold-start CA race stays closed.
- The metrics server, when `--metrics-cert-path` is set, has the same problem and
  cannot be delayed (it is built into the manager). It takes its certificate
  from a `GetCertificate` that reads the files per handshake, so it starts first and
  follows rotation; scrapes fail until the files exist.
- Whether a start-up failure before leading should hold the lease is moot: with no
  failure before the certificate exists, nothing exits holding it. A later failure
  still holds it for up to the lease duration, which is the accepted ADR 0063
  trade-off.
- `logCertStatusBeforeStart` says the server waits, not that restarts are
  expected. ADR 0031's "one or two restarts" no longer holds.

`ichor init` waits for the webhooks for 30 min by default (`WebhookWait`,
separate from the 15 min node wait), and reports the last real server answer
when that wait ends rather than the client's own "rate limiter ... deadline
exceeded".
