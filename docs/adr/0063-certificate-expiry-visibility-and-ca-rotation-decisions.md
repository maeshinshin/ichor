# 0063. CA expiry is read from the secrets bundle and published; CA rotation is decided here and not yet built

Status: Accepted · Date: 2026-10-26 · Design section: Backup, recovery, certificates
Decided: `CertExpiryReconciler` reads the CAs in the secrets bundle (talos, kubernetes, kubernetes-aggregator, etcd) and publishes each `notAfter` in `TalosCluster.status.certificates` and as `ichor_certificate_expiry_timestamp_seconds`; `talosctl rotate-ca` is not reused whole; rotating the CAs does not change the etcd backup key; the `TalosOperation` kind `CARotation` is decided below but is deliberately not implemented in this change

## Context

The design asks for two things under "Backup, recovery, certificates": Talos and Kubernetes CA
rotation "through the same machinery as upgrades", and expiry dates "visible in status, the UI and metrics".
Neither existed; `pkg/clusterlock`'s comment only promised that rotation would use the lock.

## Decision 1: what is built, and where expiry is read from

**Source: the bundle's CAs, not the nodes.** A node's leaf certificates (apid, trustd, kubelet, API server) are
issued and renewed by Talos itself; the CAs are what nobody renews, and they are what a rotation replaces
(`CAValidityTime`, `pkg/machinery/config/generate/secrets/ca.go`, is the default lifetime of a generated CA). The bundle
is the authority for them, is already loaded by every controller (`loadBundle`, ADR 0011), and reading it needs no
dial: the dates stay visible while nodes are down, which is exactly when someone asks. Ichor's own client
certificate needs no tracking: `dialAdmin` (`internal/controller/talosclient_machinery.go`) mints a fresh one on every
dial with `GenerateTalosAPIClientCertificate` (`TalosAPIDefaultCertificateValidityDuration`, one year), so it can never
be the expired one. The remaining lock-out risk is the CA itself, which is what is published.

**Surfaces.** `status.certificates` (a list keyed by `name`, each with `notAfter`; no condition, because what counts
as "soon" is a policy the alert rule owns) and the gauge `ichor_certificate_expiry_timestamp_seconds{namespace,name,
certificate}`, a timestamp so that `... - time() < 30*86400` is the alert, following
`ichor_etcd_backup_last_success_timestamp_seconds`. A deleted cluster's series are dropped. The reconciler is a
separate controller (`internal/controller/certexpiry.go`) so it neither touches `TalosClusterReconciler` (discovery
only) nor waits behind its slow `DiscoverAll`; it patches only `status.certificates`, re-reads hourly, and retries an
unreadable bundle after five minutes without failing the reconcile or putting anything from the Secret in a message.
The metric deliberately departs from ADR 0040's "zero controller metrics": that ADR relied on kube-state-metrics for
facts already in a CR, and ADR 0047 had already added backup metrics; a timestamp gauge keeps the alert independent of
anyone's kube-state-metrics custom-resource configuration.

**Later, on purpose.** The UI (the proto needs a field; a different unit owns it), a ready-made `PrometheusRule` in
the chart, and per-node leaf certificates. If those are wanted, the source is the node's COSI `secrets.API` resource
(`resources/secrets/api.go`, `APICertsSpec.Server`), read with the same admin dial.

## Decision 2: `talosctl rotate-ca` is not reused whole (same test as ADR 0057)

Talos v1.14.3, `cmd/talosctl/cmd/talos/rotate-ca.go` calls `pkg/rotate/pki/talos.Rotate` and
`pkg/rotate/pki/kubernetes.Rotate`. Both are public, and neither fits the backbone:

- **One blocking call over every node, no resume.** `rotator.rotate` (`talos/talos.go`, `kubernetes/kubernetes.go`) runs
  accept-new, swap, drop-old over all nodes in one function and keeps nothing but local variables. It cannot be driven
  from `TalosOperation` status. Re-running it after a crash is unsafe: `addNewCAAccepted` appends to
  `MachineAcceptedCAs` without checking, and the new CA is generated inside the CLI (`secrets.NewBundle` in
  `rotate-ca.go`), so a crash between phases loses a CA key that nodes already trust.
- **It writes secrets to its `Printf`.** `printNewCA` and `fetchCurrentCA` encode the whole `PEMEncodedCertificateAndKey`,
  key included, and `generateClients` prints the new talosconfig. Passing a discarding `Printf` works, but the
  design rule (nothing secret in logs, errors, status or Events) would then rest on a callback we do not control.
- **It patches machine configuration behind the controller** (`helpers.PatchNodeConfig`), which the next
  reconcile reads as drift (the objection that ruled out `upgrade-k8s`, ADR 0057).
- It needs `cluster.Info` built from talosctl's node discovery, and a new client per phase, with its own talosconfig
  output as a side effect.

What is reused is the procedure and the fields it proves. Three phases, each a full pass over the nodes, each
verified before the next: (1) add the new CA to `machine.acceptedCAs` / `cluster.acceptedCAs`; (2) make it the
issuing CA (`machine.ca`, `cluster.ca`; a worker gets the certificate only) and keep the old one accepted; (3) drop the
old CA from the accepted list. Kubernetes phases restart the kubelet (`PatchNodeConfigWithKubeletRestart`) and verify
every Node Ready afterwards.

## Decision 3: which CAs

`rotate-ca` rotates exactly two: the Talos API CA (`Certs.OS`) and the Kubernetes API CA (`Certs.K8s`; its long
description says the rest of the Kubernetes PKI is rotated by re-applying machine config). It does not touch the etcd CA,
the aggregator CA or the service-account key, and its generated bundle's other fields are discarded. `CARotation` follows
that scope. The etcd and aggregator CAs are visible in `status.certificates` but have no rotation here, and that gap is
stated rather than papered over.

## Decision 4: the secrets bundle and the backup key

**The etcd backup key does not change.** `etcdbackup.Passphrase` (`internal/etcdbackup/crypto.go`) derives it with HKDF from
`bundle.Cluster.Secret` (key) and `bundle.Cluster.ID` (salt) only, and a CA rotation does not touch either:
`rotate-ca.go` takes only `newBundle.Certs.OS` and `newBundle.Certs.K8s` from its fresh bundle, and the patches change
`machine.ca`, `machine.acceptedCAs`, `cluster.ca` and `cluster.acceptedCAs`. `TestPassphraseSurvivesCARotation` pins this
(same cluster, replaced `Certs`, same passphrase), so snapshots taken before a rotation still decrypt after it.

**Who updates the bundle Secret (when `CARotation` is built).** The executor, in the controller, as the last step of
the operation and never earlier; a Secret is not `spec`, so the "never mutates spec" rule holds. Order matters because
the Secret is what every dial and render reads: the new CA material is kept in the same Secret under a separate key
(`rotation.yaml`, written before phase 1, so a crash cannot lose a CA the nodes already trust), the render consumes it
to produce the accepted-CA fields for the current phase, and only after phase 3 verifies is `secrets.yaml` replaced and
`rotation.yaml` removed. That needs `update` on the one Secret in the controller namespace, a new RBAC grant to be
scoped by `resourceNames`.

**The user's local copy goes stale, and recovery depends on it.** `ichor init` wrote the bundle to
`~/.ichor/clusters/<name>/secrets.yaml`; after a rotation that file describes a PKI no node accepts any more, and
"bundle + snapshot is sufficient" (ADR 0053) holds only for the current bundle. The operation therefore does not report
success silently: it ends with a `BundleBackupStale` condition on the `TalosCluster` (cleared by an explicit CLI
acknowledgement after `ichor` re-exports the bundle) and an Event, until the user has saved the new bundle. The message
carries no secret.

## Decision 5: why `CARotation` is not implemented here

It would be the third phased executor and it changes the one thing every other controller trusts, the bundle. Doing it
properly needs, at least: a render input for accepted CAs (`pkg/render` has none, and a machine in phase 2 has a
different rendered config from one in phase 1, so the machine controller's drift check must not fight the operation), the
Secret write and RBAC above, a verified Ichor dial through each phase (the admin client certificate must come from the new
CA after phase 2), the `BundleBackupStale` flow in the CLI, and the three tests named in the brief (leak test with the
scrubber, bundle/backup consistency, crash-resume per phase). Half of that is worse than none: a partial executor
that stops between phases leaves a cluster in a state only the full executor can finish. Next unit: add
`kind: CARotation` to `TalosOperation`, `pkg/render` accepted-CA inputs, then the executor on the ADR 0026 backbone,
holding the lock through `acquireDisruptiveLock`.

## Consequences

- `TalosCluster.status.certificates` is a new list; the CRD, chart copy and deepcopy are regenerated.
- A new controller, `certexpiry`, reuses the existing RBAC (`talosclusters` get/list/watch, `talosclusters/status` patch,
  `secrets` get/list/watch in the controller namespace); no new permission.
- `go.mod` gained `github.com/kylelemons/godebug` as an indirect dependency, which the Prometheus `testutil` package the
  tests use needs.
- Until `CARotation` exists, rotation is `talosctl rotate-ca` by hand, after which the Secret must be replaced by the user
  or the controller can no longer authenticate.
