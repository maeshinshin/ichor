# 0120. The CA rotation also replaces the etcd CA and the Kubernetes aggregator CA

Status: Accepted · Date: 2026-11-04 · Design section: Backup, recovery, certificates · Touches: 0063, 0091, 0093, 0108

Decided: the `ca-rotation` operation can also replace the etcd CA and the Kubernetes aggregator (front-proxy) CA, chosen with the new, optional `spec.caRotation.targets` (`talos`, `kubernetes`, `etcd`, `kubernetes-aggregator`; absent means `talos` and `kubernetes`, as before). It is the same state machine, lock, pause/continue and finalizer, with the same three passes. The aggregator CA uses Talos's own accepted-CA list (`KubeAggregatorCAConfig`, `acceptedCAs`). Talos has no accepted-CA list for etcd, so Ichor puts both etcd CA certificates into `cluster.etcd.ca.crt` (a PEM bundle, the new key signing from Issuing on) and restarts the etcd members one at a time after each pass; the credential hand-off (AwaitingCredentials) happens only when the Talos or Kubernetes CA is among the targets. The etcd backup key does not change. Before the first hardware run the etcd bundle is a reading of Talos v1.14.5 source, not of its documentation; ADR section "Hardware verification" says what must be seen before it is trusted.

## Context

ADR 0108 (S6) left the etcd CA and the aggregator CA out of the rotation: "the etcd CA rotation needs every member restarted with both CAs trusted in a defined order". The user decided on 2026-11-04 that both ship in v0.1.0. The service-account signing key and the secretbox key are a different mechanism (ADR 0121).

### What Talos v1.14 offers (version in go.mod: `siderolabs/talos/pkg/machinery` v1.14.5)

- `talosctl rotate-ca` rotates the Talos API CA (`--talos`) and the Kubernetes API CA (`--kubernetes`) only. [Talos CA rotation](https://docs.siderolabs.com/talos/v1.14/security/ca-rotation) lists the general flow (generate, accept, issue, refresh, drop) and covers those two; `cmd/talosctl/cmd/talos/rotate-ca.go` and `pkg/rotate/pki/{talos,kubernetes}` contain nothing for etcd or the aggregator. The same page says: "The aggregator CA is configured separately through `KubeAggregatorCAConfig`" and gives no procedure for it.
- **Aggregator CA.** The typed document `KubeAggregatorCAConfig` (`pkg/machinery/config/types/k8s/aggregator_ca.go`) has `issuingCA` and `acceptedCAs`; the issuing CA is added to the accepted list implicitly. Talos renders `acceptedCAs` into `aggregator-ca.crt`, which is kube-apiserver's `--requestheader-client-ca-file` (`controllers/k8s/render_secrets_static_pod.go`, `control_plane_final.go`), and signs the front-proxy client certificate (`--proxy-client-cert-file`) with `issuingCA` (`controllers/secrets/kubernetes_dynamic_certs.go`). kube-apiserver publishes the accepted bundle in the ConfigMap `kube-system/extension-apiserver-authentication` for extension API servers ([Configure the Aggregation Layer](https://kubernetes.io/docs/tasks/extend-kubernetes/configure-aggregation-layer/)). The document exists for control planes only and only with a version contract that has the multi-document Kubernetes configuration (`MultidocKubernetesConfigSupported`, v1.14); earlier contracts use `cluster.aggregatorCA`, a single CA with no accepted list, and the two forms conflict (`V1Alpha1ConflictValidate`).
- **etcd CA.** The only field is `cluster.etcd.ca` ([config reference](https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config)). There is no accepted-CA list. In the source: `EtcdConfig.CA()` feeds `secrets.EtcdRoot` (`controllers/secrets/root.go`); `etcd/pki.go` writes `EtcdCA.Crt` verbatim to `/system/secrets/etcd/ca.crt`; etcd runs with `trusted-ca-file` and `peer-trusted-ca-file` pointing at that file (`system/services/etcd.go`); `secrets/etcd.go` makes the server, peer, admin and kube-apiserver client certificates from the CA with `x509.NewCertificateAuthorityFromCertificateAndKey`, which parses only the first PEM block of `Crt` (`pem.Decode`) and the key; kube-apiserver gets the same file as `etcd-client-ca.crt` (`--etcd-cafile`). Nothing restarts etcd when the files change, and nothing validates the CA's content beyond "present". Consequence: with a plain replacement of `cluster.etcd.ca` a member on the new CA and a member on the old one cannot talk to each other, so rotating the CA member by member loses quorum at the middle restart.

## Decision

### 1. Targets (API)

Append-only: `TalosOperation.spec.caRotation` (`TalosOperationCARotationSpec`, optional) with `targets []string` (enum `talos`, `kubernetes`, `etcd`, `kubernetes-aggregator`; unique; at least one). Absent or empty means `talos` and `kubernetes`, so every existing operation, script and UI call is unchanged. The names are the ones `status.certificates` of the cluster already uses for expiry (ADR 0063). `status.caRotation.certificates` gets entries named like the targets, and two new status fields: `etcdRestarted` and `etcdRestartedAt` (below). The stage enum is unchanged. The wire (`CreateOperationRequest`, `PlanOperationRequest`) gets `repeated string ca_targets`.

A separate operation kind was rejected: it would need its own lock handling, finalizer, machine-controller quiescing, pause/continue, `rotation.yaml` and bundle replacement, which are exactly what the existing machine provides, and a second kind could run beside a first one. One operation per cluster at a time stays true.

The targets are decided once, when the operation starts: the new CAs are generated for the targets and written to `rotation.yaml` (optional keys `talos`, `kubernetes`, `etcd`, `aggregator`; the set of keys present is the set of targets). A restarted controller, or a rotation started by an older Ichor, reads the targets from `rotation.yaml`, not from the spec.

### 2. Stages and rendered configuration

The stages and their meaning stay: Accepting (the new CA is trusted, the old one issues), Issuing (the new one issues, the old is trusted), Dropping (only the new one), Finalizing. Per target, in the typed documents:

| target | Accepting | Issuing | Dropping, Finalizing |
| --- | --- | --- | --- |
| talos | `machine.acceptedCAs` + new | `machine.ca` new, accepted old | `machine.ca` new |
| kubernetes | `KubeAPIServerCAConfig` accepted + new | issuing new, accepted old | issuing new |
| kubernetes-aggregator (control planes) | `KubeAggregatorCAConfig.acceptedCAs` + new | `issuingCA` new, `acceptedCAs` old | `issuingCA` new |
| etcd (control planes) | `cluster.etcd.ca`: certificate = old then new, key = old key | certificate = new then old, key = new key | certificate = new, key = new key |

The etcd rows rely on two facts read in Talos's source (above): the certificate field is written verbatim into the trust file, which etcd and kube-apiserver read as a PEM bundle, and the signing CA is the first block with the key. The first row therefore keeps the old CA signing while both are trusted; the second changes the signer while both are still trusted; the third drops the old one. Dropping and Finalizing render byte for byte what a bundle holding the new CAs renders (a test pins it), so the machine controller sees no drift afterwards. A target that is not rotated renders exactly as before.

Refused at the start (plan `blocked` and executor preflight): `kubernetes-aggregator` on a cluster whose version contract is older than v1.14 (no accepted list exists there); `etcd` with exactly two control planes (restarting one member loses the quorum of a two-member etcd).

### 3. etcd: restart the members, one at a time, after each pass

Applying the configuration changes the files and the leaf certificates, but the running etcd keeps what it loaded, so after each of the three passes the controller restarts the etcd service of every control plane, one at a time, in the order the control planes are visited:

1. The restart starts only when every machine already runs the stage's configuration (the apply and the restart are separate reconciles) and only when the cluster is healthy: every control plane's `etcd` service is Running and healthy, its member list has one member per approved control plane, no alarm is set, and the node's etcd PKI is the stage's: the root CA the node reports (COSI `secrets.EtcdRoot`) holds the expected certificates in the expected order and its server certificate (`secrets.Etcd`) is signed by the expected CA. Otherwise the operation pauses (`Blocked`) and tries again every 30 s; nothing is restarted.
2. If the member leads, it forfeits leadership first (`EtcdForfeitLeadership`), then `ServiceRestart("etcd")`.
3. The name goes into `status.caRotation.etcdRestarted` and the time into `etcdRestartedAt`; the next member is restarted only after `etcdRestartSettle` (15 s) and a new health check. `etcdRestarted` is emptied when the stage changes. A controller killed after the restart but before the status write restarts that member once more, which is harmless.
4. The pass is verified (as before: Ichor reaches each node, every Node is Ready) only after the last member has restarted and the final health check passes.

Because both CAs are trusted by every member from the first pass on, and every member presents a certificate either CA vouches for, a member on an earlier or later step of the roll can always talk to the others: during each roll at most one member is down, which is an ordinary rolling restart, quorum holds with three or more members, and a single control plane merely has its API unavailable while its only member restarts (the plan says so: `kubernetes_api_unavailable`). Three rolls are needed, not one: after Accepting every member trusts both CAs; after Issuing every member presents a certificate of the new CA (kube-apiserver then trusts both); after Dropping only the new CA is trusted. kube-apiserver reads its etcd client CA and certificate from the same Talos secrets, and Talos restarts its static pod on every change, so each pass also restarts kube-apiserver once per control plane; the existing Node-Ready verification covers it.

### 4. Aggregator: wait for the extension API servers, never restart them

kube-apiserver restarts on its own (Talos re-renders the static pod). Before Issuing, the controller reads `extension-apiserver-authentication` through the admin kubeconfig a Talos node issues (as the `kube-root-ca.crt` check does) and waits until its `requestheader-client-ca-file` holds the new CA, then waits `ClientSettleDelay` (2 minutes; the clock is a condition transition time, so it survives a restart) so that extension API servers that reload the ConfigMap have the new CA before the front-proxy certificate is signed by it. Ichor restarts none of them (they are workloads such as metrics-server; ADR 0101): the plan and the completion message say that an extension API server that read the CA only once must be restarted by the owner. The wait is Verifying-stage of Accepting, not a new stage.

### 5. Credentials hand-off, secrets bundle, backups

- **AwaitingCredentials** exists for the operator's talosconfig and kubeconfig (ADR 0091). When neither `talos` nor `kubernetes` is a target nothing the operator holds changes, so the stage is skipped (Accepting goes to Issuing) and the CLI/UI do not ask for a hand-off. `CARotationHandoffToken` is unchanged in form; it also names etcd and aggregator entries when present.
- **Bundle.** `Certs.Etcd` and `Certs.K8sAggregator` are replaced in the one Secret update that replaces the others (`Rotation.Apply` replaces what `rotation.yaml` holds and nothing else). `rotation.yaml` holds the keys of the new CAs from before any node is told anything, as in ADR 0063. A key that is not in `rotation.yaml` is never touched.
- **BundleBackupStale.** Raised as before. `BundleFingerprint` (Talos and Kubernetes CAs) is unchanged and is still what the credentials code compares; a new `ExportFingerprint` adds the etcd and aggregator CAs, and `bundle-export` and the cert-expiry reconciler use it, so an export made before an etcd-only rotation does not count as current. Cost: an export annotation written by an earlier Ichor no longer matches, so a cluster that has BundleBackupStale up at the moment of the upgrade needs one more `bundle-export`.
- **etcd backups.** The backup key derives from `Cluster.Secret` and `Cluster.ID` (ADR 0063, ADR 0108 section 4), which a CA rotation does not touch; snapshots contain no PKI. Snapshots taken before the rotation stay readable and restorable (a restore renders the configuration with the then-current CAs). `TestPassphraseSurvivesCARotation` still pins it, and the controller test asserts the cluster ID and secret are unchanged. The secretbox key stays out of scope (ADR 0121).
- **A fresh snapshot first.** The plan warning for an etcd rotation tells the user to take an etcd snapshot before starting (`ichor etcd backup now NAME`). Ichor does not require one: a missing backup is the user's decision for their cluster, not something Ichor forces (the "warn, do not force" rule for the managed cluster).

### 6. Failure and rollback

Every step is idempotent and re-derived from the nodes and from status (ADR 0063): a failed apply, a node that does not answer, a Node not Ready, an etcd member that does not come back, a refused dry run: the operation is Paused with `CARotation=False/Blocked` and a message, the next stage is never started, and it retries every 30 s. No member is restarted while any is unhealthy, so at most one member is down at any time. A rotation is never rolled back by Ichor automatically; it only moves forward. Until Dropping has started, every member and kube-apiserver still trust the old CA, so a rotation that cannot proceed can be abandoned by hand: remove `rotation.yaml` from the bundle Secret and the operation's finalizer (ADR 0063), after which the machine controller renders the old CAs again; the members pick that up at their next restart (expected, not exercised on hardware). After Dropping started, the old etcd CA is gone from the nodes that were already rolled: continue, do not revert.

### 7. UI and CLI

- CLI: `ichor cluster rotate-ca NAME [--ca talos,kubernetes,etcd,kubernetes-aggregator] [--all]`; default unchanged. `--yes`, `--follow` as before. The plan lists the targets, the stages, the etcd restarts and the warnings; the credential hand-off runs only when `talos` or `kubernetes` is a target.
- UI: the start dialog for a CA rotation shows four checkboxes (Talos API and Kubernetes API checked, etcd and aggregator unchecked) and sends `ca_targets`; the operation page lists `status.caRotation.certificates` with the new names and shows the etcd members restarted so far; the credentials card (`CARotationCredentials`) appears only for rotations that include `talos` or `kubernetes`.
- The cert-expiry message no longer says the two CAs "are not covered".

## Rejected

- **Replacing `cluster.etcd.ca` in one pass (all members at once, or one by one).** At once is an outage of the whole control plane; one by one, with a single CA per member, splits the cluster by CA at the middle restart.
- **Issuing etcd leaf certificates from the new CA and keeping the old trust through a separate file.** Talos has no field for it and Ichor may not write raw files (machine config is typed only, no raw escape hatch).
- **Treating the etcd CA as unrotatable and documenting it.** The user decided it ships in v0.1.0.
- **A restart of every member at the same time per pass, or no restart in Accepting.** The certificates are loaded at start; a member that never restarted after Accepting would not trust the new CA when Issuing makes the others present it.
- **A hard requirement for a recent etcd snapshot.** Warned about, not enforced (above).
- **Restarting extension API servers.** Not Ichor's workloads (ADR 0101).

## Risks

- The etcd bundle depends on Talos behaviour that is not in its documentation (first PEM block signs, whole field is the trust file, etcd and kube-apiserver read bundles). If a Talos release changes it, the etcd target would split the cluster; Ichor's own check (the node's `secrets.Etcd` is signed by the expected CA, the trusted list is the expected one) pauses the rotation before any restart if the node does not behave as read. The target is therefore also the one to run first on a disposable cluster.
- Three etcd rolls and three kube-apiserver restarts per control plane; a one-control-plane cluster has its Kubernetes API down during each member restart.
- Extension API servers that read the aggregator CA once need a restart by their owner.
- `ClientSettleDelay` adds two minutes to a rotation that includes the aggregator CA.

## Test

envtest (`internal/controller`, a fake node per machine as in ADR 0063): the stage sequence, rendered documents per stage and role for etcd and aggregator (typed: `cluster.etcd.ca` certificate order and key, `KubeAggregatorCAConfig`), Dropping equal to the replaced bundle's render, rotation of only etcd skipping AwaitingCredentials, etcd members restarted in order with the health gate and leader forfeit, a restart not repeated after a controller restart, an unhealthy member holding the roll, two control planes and a legacy contract refused, the aggregator wait, `rotation.yaml` keys and the replaced bundle, backup key unchanged; `pkg/render` goldens per stage; CLI flags and plan output; UI selection and card.

## Hardware verification

Not run. Steps (3 control planes and 1 worker, contract v1.14, a throwaway cluster; `kubectl --context admin@talos-cluster01` for the outer cluster only):

1. `ichor etcd backup now NAME` and wait for the snapshot. Note `talosctl etcd members` and a ConfigMap written before the rotation.
2. `ichor cluster rotate-ca NAME --ca etcd --yes --follow`. Expect: no AwaitingCredentials; after Accepting, `talosctl -n <cp> read /system/secrets/etcd/ca.crt` holds two certificates (old first); after Issuing, new first; after Dropping, one (the new). Each roll: only one `etcd` service restarts at a time (`talosctl service etcd` on each), `talosctl etcd members` never loses a member, `kubectl get --raw /readyz` recovers within a minute per control plane.
3. After Done: `talosctl etcd status` on every control plane, check `talosctl read /system/secrets/etcd/etcd.crt | openssl x509 -noout -issuer -fingerprint` against the new CA and `talosctl get etcdrootsecret -o yaml` (public part). The ConfigMap from step 1 is still readable; an etcd backup taken after the rotation succeeds; an older snapshot is listed and its key still decrypts it.
4. `ichor cluster rotate-ca NAME --ca kubernetes-aggregator --yes --follow` on a cluster with metrics-server: `kubectl top nodes` works before, after Accepting and the settle wait, after Issuing and after Done; `kubectl get cm -n kube-system extension-apiserver-authentication -o jsonpath='{.data.requestheader-client-ca-file}'` holds two certificates, then one.
5. `ichor cluster rotate-ca NAME --all --yes --follow` once on a fresh cluster, then `ichor cluster bundle-export NAME` clears BundleBackupStale.
6. Negative: kill the controller pod during an etcd roll; the roll resumes, no member is restarted twice in a row without a health check. Power off one control plane before starting; the operation refuses to start.
7. If step 2 shows a member that does not trust a certificate signed by the other CA, stop: the etcd bundle assumption is wrong for that Talos build; do not use the `etcd` target and record the Talos version.
