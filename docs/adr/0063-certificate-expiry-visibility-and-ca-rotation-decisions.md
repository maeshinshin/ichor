# 0063. CA expiry is read from the secrets bundle and published; CA rotation is decided here and built as the `ca-rotation` operation

Status: Accepted (the stage sequence and the operator's credentials are amended by ADR 0091) · Date: 2026-10-26 · Design section: Backup, recovery, certificates
Decided: `CertExpiryReconciler` reads the CAs in the secrets bundle (talos, kubernetes, kubernetes-aggregator, etcd) and publishes each `notAfter` in `TalosCluster.status.certificates` and as `ichor_certificate_expiry_timestamp_seconds`; `talosctl rotate-ca` is not reused whole; rotating the CAs does not change the etcd backup key; the `TalosOperation` kind `CARotation` is decided below but is deliberately not implemented in this change; the `ca-rotation` TalosOperation kind is built as its own state machine on the ADR 0026 backbone: three verified passes, the new CAs saved to the Secret's `rotation.yaml` before any node is told, `secrets.yaml` replaced last, `BundleBackupStale` until `ichor export bundle` (see "Implementation")

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

**Later, on purpose.** The UI (the proto needs a field), a ready-made `PrometheusRule` in
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
`machine.ca`, `machine.acceptedCAs`, `cluster.ca` and `cluster.acceptedCAs`. Snapshots taken before a rotation
therefore still decrypt after it.

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
CA after phase 2), the `BundleBackupStale` flow in the CLI, and three tests: a leak test with the scrubber, bundle/backup consistency, and crash-resume per phase. Half of that is worse than none: a partial executor
that stops between phases leaves a cluster in a state only the full executor can finish. Next: add
`kind: CARotation` to `TalosOperation`, `pkg/render` accepted-CA inputs, then the executor on the ADR 0026 backbone,
holding the lock through `acquireDisruptiveLock`.

## Consequences

- `TalosCluster.status.certificates` is a new list; the CRD, chart copy and deepcopy are regenerated.
- A new controller, `certexpiry`, reuses the existing RBAC (`talosclusters` get/list/watch, `talosclusters/status` patch,
  `secrets` get/list/watch in the controller namespace); no new permission.

- Until `CARotation` exists, rotation is `talosctl rotate-ca` by hand, after which the Secret must be replaced by the user
  or the controller can no longer authenticate.

## Implementation (the `ca-rotation` kind)

Decisions 2 to 5 stand. Decision 5 said what the executor needed; this is how each part was built, and where the build
differs from what was sketched.

**Entry.** `TalosOperation.spec.kind: ca-rotation`, started through `OperationService` like every other kind (ADR 0068, Decision 7):
`ichor rotate ca NAME` (plan, confirmation, `CreateOperation`; backend or in-process fallback) and the UI's "Rotate CAs"
button on the cluster page. The command no longer writes the custom resource through the kubeconfig; there is one path. The UI's
progress page shows `status.caRotation.stage` and, once complete, that the secrets bundle on the user's machine is stale.

**State machine (`internal/controller/ca_rotation.go`).** Like shutdown, the backbone takes the cluster lock through
`acquireDisruptiveLock` (the only `Acquire`) and then dispatches to `advanceCARotation`. Everything is in
`status.caRotation`: `stage` (`Accepting`, `Issuing`, `Dropping`, `Finalizing`, `Done`; empty before the new CAs are saved) and, per
rotated CA (`talos`, `kubernetes`), `previous`, `next`, `issuing` and `accepted` as SHA-256 fingerprints of the certificates. The
stage names what every node is being given, so which CA is accepted and which issues is always readable from status. Which
machines a stage still has to visit is not stored: it is re-derived from the node (its current configuration hash against the
configuration rendered for the stage), so a controller killed at any point resumes from status, and a pass that was half done
repeats no apply. `status.machines` shows the stage under way and is emptied when the stage changes. Both CAs are rotated in the same
passes (one apply per machine per stage), where `talosctl rotate-ca` runs the Talos pass set and then the Kubernetes one.

1. *Preflight* (stage empty). Every approved machine must answer Ichor, must already run exactly the configuration Ichor renders for
   it (so no unrelated pending change is swept into the rotation), and every Kubernetes Node must be Ready. Otherwise the
   operation pauses and nothing is written, not even a CA. A machine that is down when the old CA is dropped could never be
   reached again, which is why the check is at the start and again at every stage.
2. *Save the new CAs.* `rotation.yaml` (certificate and key for both) is written into the bundle Secret with an `Update` that
   fails if it already exists, and only then is `stage: Accepting` written. A start that died between the two is resumed by
   adopting that `rotation.yaml`; no node has been told anything at that point.
3. *Accepting, Issuing, Dropping.* Each stage renders the machines (below), applies to each that differs (dry run, then
   `ApplyNoReboot`, control planes first), and in a later reconcile verifies before the stage advances: Ichor can dial every
   machine with the stage's credentials (`Version`), and every Kubernetes Node is Ready (read through the admin kubeconfig a Talos
   node issues, `NodesReady`). The verification is a separate reconcile from the applying one, so the kubelet restart a CA change
   causes has begun before readiness is read (`shortcut:` this does not observe the restart itself, as `talosctl`'s helper does
   through the kubelet service resource; add that if a restart is ever missed).
4. *Finalizing.* One Secret update puts the new CAs into `secrets.yaml` and removes `rotation.yaml`, and fails if the Secret changed in
   between. Then `BundleBackupStale` is raised, and every machine is checked against the replaced bundle: Ichor dials it with the
   plain bundle (as every other controller will) and its configuration equals what the replaced bundle renders. Only then `Done`,
   the lock is released and the phase is `Completed`. A restart after the Secret was replaced but before status learned of it
   recognises the new CAs by fingerprint and carries on.

**A step that cannot be verified never advances.** Ichor not reaching a machine, a Node not Ready, a refused dry run or apply, a
render failure: the operation is `Paused` with a `CARotation` condition (`False`, reason `Blocked`, a message naming the cause) and is
tried again every `operationRequeueInterval`, so it continues by itself when the cause is gone. This is not the failure pause of the
other kinds (`failureReason`, which is never retried): a rotation stopped for good halfway is the state this ADR refuses to create,
and a retry is always safe because each step is idempotent.

**How Ichor stays connected.** Ichor builds its Talos client certificate from the bundle on every dial. During a rotation the dial
uses a derived bundle: the signing CA and key are the one the stage's nodes accept, and the trusted server CA list holds both CAs
(`dialBundle`; `GenerateTalosAPIClientCertificate` signs with the first certificate and key, and the trust list is a PEM bundle).
Accepting is applied with the old client certificate, which every node still accepts; every later stage with the new one, which the
Accepting verification proved the nodes accept.

**Render input (`pkg/render/carotation.go`).** `SystemInputs.CARotation{Stage, New}`, set by the executor only (never by
`ResolveSystemInputs`). The bundle passed to `Render` stays the old one. Accepting renders from it and adds the new CAs to
`machine.acceptedCAs` and the Kubernetes accepted list; Issuing renders from a copy of the bundle with the new CAs and adds the old
ones as accepted; Dropping and Finalizing render from that copy with nothing extra. The Kubernetes accepted list is written through
machinery's own `rotatepatcher`, so the single- or multi-document form follows the version contract. The Dropping
configuration is byte for byte what a bundle already holding the new CAs renders, for both roles, which is what leaves the machine
controller no drift when the rotation ends.

**The machine controller does not fight the stages.** While a `ca-rotation` operation is under way (a stage is set and the
operation is not `Completed`), the machine controller sets `Ready=Unknown/CARotationInProgress` and does nothing else: it neither
renders, dials nor applies, so it cannot revert a stage, and its dial (made with the bundle, which names the old CAs) cannot fail
against nodes that already serve the new ones. The check reads uncached. The disruptive-work gates (`shutdownBlocker`, used by
`acquireDisruptiveLock` of both controllers and by machine removal) refuse other operations and removals for the same reason, with the
reason `ClusterCARotating`. The `TalosOperation` carries a finalizer, removed when the rotation is `Done`, because the stage is the only
record of which CA the nodes accept: deleting the operation midway would lose it. If a rotation must be abandoned by hand, remove the
finalizer knowing that the nodes are then in the stage status last showed and `rotation.yaml` is still in the Secret.

**Secret write and RBAC.** `update` on Secrets in the controller namespace was added to the manager's namespaced Role (config and
chart). It is broader than Decision 4 intended: `resourceNames` cannot name the bundle Secret, whose name is `spec.secretsRef.name`,
chosen by the user. Reads of the bundle, `rotation.yaml` and the machine gate go through the API reader. `talosoperations/finalizers`
was added too. Amendment (write scope): the broad `update` is kept. A required label (`ichor.maesh.dev/secrets-bundle=true`) was rejected: RBAC cannot filter by label, so it adds no API-side protection, and it would break every existing bundle. Since ADR 0096 only `talosclusters/secret-references` holders can change `secretsRef`,  and the executor enforces in code (`caRun.updateBundle`) that it writes only the Secret `secretsRef` names in the controller namespace.

**`BundleBackupStale`.** Raised on the `TalosCluster` (condition, `reason: CARotated`) and as a Warning Event by the executor at
Finalizing. The message names the command and carries no secret. `ichor export bundle NAME` reads the live bundle from the
Secret (refusing while `rotation.yaml` exists), saves it to `~/.ichor/clusters/<name>/secrets.yaml`, reads the file back, and only if
it holds the live bundle sets the annotation `ichor.maesh.dev/bundle-exported` on the cluster to the bundle's fingerprint (a hash of
the two CA fingerprints). `CertExpiryReconciler` lowers the condition when the annotation equals the live bundle's fingerprint; an
export of the pre-rotation bundle does not. The CLI writes metadata, not `spec`, and the controller still owns every status.

**Backups.** Not touched: `etcdbackup.Passphrase` derives from `Cluster.Secret` and `Cluster.ID`, which `Rotation.Apply` leaves as they
are, as it does the etcd CA.

**Scrubbing.** The scrubber is applied to every message `caRun.status` writes, so dial errors that echo a CA key never reach a status
snapshot, Event, cluster object or returned error.

**Not built, and the limits.**

- Between Issuing and Finalizing the bundle in the Secret names the old CAs while nodes serve the new ones, so the other controllers
  that dial with it (etcd backup, discovery, the UI backend's read-only client) fail to connect and retry until Finalizing. Only the
  machine controller is quiesced explicitly. A backup that falls in that window fails and is taken by the next schedule.
- `status.certificates` follows the replaced bundle at the next hourly read of the cert-expiry reconciler, not at once.
- Held machines (`upgrade-hold`) are not skipped: a machine left out would be locked out when the old CA is dropped.
- The etcd and aggregator CAs and the service-account key are not rotated (Decision 3).

## Amendment: in-cluster clients and the serving-CA switch

Right after a rotation reaches Done, the Ichor controller pod can crash-loop with `x509: certificate signed by unknown authority` against the in-cluster API until its CA file refreshes (about a minute). Every in-cluster client has the same exposure.

- Talos renders kube-controller-manager with `--root-ca-file` pointing at a file that holds the **issuing** Kubernetes CA alone (`render_secrets_static_pod.go`, `IssuingCA`; the flag is merge-denied in `control_plane_final.go`). `kube-root-ca.crt`, from which every pod's projected `ca.crt` is made, therefore holds the old CA through Accepting and only the new one from Issuing on. It never holds both, and Ichor cannot make it (no typed field reaches that flag). The kube-apiserver serving certificate is signed by the issuing CA too, so it switches in the same pass.
- A pod's file follows only after controller-manager restarts and republishes the ConfigMaps and the kubelet refreshes the projected volume (about a minute). client-go v0.36 re-reads `ca.crt` (`ClientsAllowCARotation`, beta, on by default) but at most once per 5 minutes and only on a request (`transport/ca_rotation.go`); clients on older libraries, or that read the file once, never do. A process that exits in the window (for Ichor, probably leader election lost because renewals fail the TLS check) restarts onto the not yet refreshed file and fails at start until the kubelet catches up.
- Decision: the window cannot be removed, so it is bounded and made visible. After Issuing is verified the controller does not start Dropping until `kube-root-ca.crt` carries the new Kubernetes CA in every namespace (the condition message names what it waits for), and then for `caClientSettleDelay` (2 minutes, timed from the `SettlingClients` condition's transition time, so it survives a controller restart). It reads through the Talos-issued admin kubeconfig, like the Node readiness check.
- Limitation, stated in the plan text: in-cluster clients that load the CA once may need a restart. Ichor does not restart workloads.

## Addendum: expiry alerting

`CertExpiryReconciler` also sets the `TalosCluster` condition `CertificatesExpiring` (True while any CA expires within the warning window or has expired; its message names each CA and its UTC date, not the days left, so it does not rewrite status every hour). The window is the controller flag `--certificate-expiry-warning-days` (default 90), not a spec field: it is operator policy, not cluster intent, and a flag needs no CRD change. The chart adds the `IchorCertificateExpiring` rule (`prometheus.rules.enabled`, window `prometheus.rules.certificateExpiryDays`, default 90, to be kept equal to the flag) on `ichor_certificate_expiry_timestamp_seconds`; the UI lists a cluster in Overview's "Needs attention" and shows the message on the cluster page. This supersedes "no condition" in Decision 1.

## Amendment: Ichor restarts the clients that keep the old CA

The `kube-root-ca.crt` wait and the settle delay are not enough: after Done the CNI and CoreDNS can keep logging `x509: certificate signed by unknown authority` indefinitely (new Services do not resolve), kube-proxy recovers after about 5 minutes, and ichor-ui cannot reach the API for about 5 minutes after Issuing (client-go re-reads the CA at most every 5 minutes), which also blocks a following `rotate-ca`.

- Decision: in Finalizing, after the old CAs are dropped and the replaced bundle is verified, and before Done, the controller restarts once the clients known to keep the old CA: the `coredns` Deployment and the `kube-proxy`, `cilium` and `kube-flannel` DaemonSets in kube-system (the same names the restore's agent check uses; one that does not exist is skipped), and the `ichor-ui` Deployment in the controller's namespace. The restart is a rollout restart: a merge patch of the pod template's `kubectl.kubernetes.io/restartedAt` annotation, plus `ichor.maesh.dev/ca-rotation` set to the operation's UID.
- This is a write to those workloads and is in scope in the same way as the restore's one-time pod restart: Ichor bootstraps the CNI but does not operate it, and this is recovery from Ichor's own action, done once. It never mutates `spec` of any Ichor resource.
- Idempotent across controller restarts without a status field: a workload whose pod template already carries this operation's UID is not patched again. The wait is bounded by `caClientRestartTimeout` (5 minutes, timed from the `RestartingClients` condition's transition time, so it also survives a restart). Workloads not available by then do not hold the rotation: it completes and the `CARotation` condition's message names what is not back.
- RBAC: `get;patch` on the three DaemonSets and on `coredns`, by `resourceNames`, in the manager ClusterRole (a ClusterRole rule cannot be limited to kube-system, so the same names in another namespace are also patchable); `patch` on `ichor-ui` in a Role in the controller's namespace.
- Limitation, stated in the plan text: other in-cluster clients that load the CA once may still need a restart.
- Ordering: restarting all four at once while the API server restarts breaks Services and DNS for about a minute. The restarts are issued only after the Finalizing verification passes (replaced bundle accepted, every Node Ready) and one at a time, each after the previous is available again: CoreDNS, kube-proxy, the CNI, ichor-ui. The 5-minute bound covers the whole sequence; workloads not yet restarted at the timeout are named as not back.
- Stale Ready: the Nodes still read Ready right after Dropping, then the kubelets restarted and CoreDNS lost its replicas. Before the first restart the controller reads the Nodes live (a fresh admin kubeconfig, never a cache) and requires them Ready continuously for `caKubeletSettleDelay` (90 s, longer than the 40 s node-monitor grace plus a kubelet restart); a NotReady read resets the clock. It waits (`AwaitingNodes`, `SettlingNodes`), it does not Pause. Node `lastTransitionTime` is not used: a kubelet that restarts inside the grace period never changes it.

## Amendment: the controller trusts the new Kubernetes CA ahead of the switch

Without further measures the controller restarts once or twice per rotation. At Issuing the API server serves a certificate from the new Kubernetes CA, the running controller's lease renewal fails `x509`, leader election is lost and the process exits; the restarted container (same pod) mounts the old projected `ca.crt`, which the kubelet cannot refresh while the API server restarts, so the 5-minute start-up wait times out and it exits again.

- Decision: the controller already generates the new Kubernetes CA before Accepting, so its own client trusts it in advance. `internal/kubetrust` keeps a process-wide pool, the in-cluster `ca.crt` (re-read on every handshake) plus extra CA certificates added at runtime. `rest.Config.Transport` is replaced by a transport whose TLS config verifies in `VerifyConnection` (the stock check is switched off only so this one can run): it checks the chain against the pool and the API host name, taken from the config, not from the connection, because the connection's server name is empty for an IP host. Verification is never skipped. Configs with client certificates, exec/auth providers, `insecure` or their own transport are left alone.
- Every pass of a CA rotation that can read `rotation.yaml` adds the new Kubernetes CA's certificate (public part only; the key never leaves the Secret) to the pool and writes the pool's certificates to `/var/run/ichor/trust/kubernetes-ca.crt`, an `emptyDir` mounted into the manager container. Start-up loads that file before the API-server wait, so a restarted container trusts the new CA at once. Publishing failing is logged and does not stop the rotation.
- The file is not pruned after Done: an extra trusted CA that signed nothing the API server still serves is harmless, and the pod's volume ends with the pod.
- Limitation: a pod that is rescheduled during Issuing loses the file and falls back to the projected `ca.crt`, as before. Other in-cluster clients are unchanged (see the previous amendments).

## Amendment: leader election outlives an API server restart

On a single-control-plane cluster the controller still restarts after the trust amendment. At Dropping the API server is down for the whole static-pod restart, longer than controller-runtime's default RenewDeadline (10 s); the lease renew got `connection refused`, then `context deadline exceeded`, and the manager exited with `leader election lost`.

- Decision: `LeaseDuration` 120 s, `RenewDeadline` 90 s, `RetryPeriod` 10 s (`cmd/leaderelection.go`).  A 60-90 s outage no longer loses the lease. client-go requires LeaseDuration > RenewDeadline > RetryPeriod x 1.2.
- Cost: after a crash (not a voluntary stop) a second replica waits up to `LeaseDuration` (120 s) before taking over, where the default took 15 s.
- `LeaderElectionReleaseOnCancel` stays off: the process outlives the manager in `WaitForRestoreTails` (ADR 0089, up to 70 minutes), and releasing the lease there would let a second replica act mid-restore. Voluntary hand-offs therefore also wait out the lease.
- Readiness now includes the webhook server (`StartedChecker`), because a replaced pod was 1/1 Ready while admission calls to it still failed.
- Diagnostics: start-up logs how many extra CA certificates it loaded from the trust file, `Publish` logs each newly trusted certificate (subject and validity only), and the API wait logs each failed probe (at most one line per 15 s) and names the last error when it gives up.

## Amendment: the trust pool is pruned, and every replica keeps its own

Two gaps in the trust amendment above: the pool only grew, so after a second rotation the first rotation's CA (possibly exposed, which is why people rotate) stayed trusted until the pod was recreated; and the follower replica never ran the rotation, so it never published and its webhooks and informers failed x509 until its projected `ca.crt` refreshed.

- Supersedes "The file is not pruned after Done". `Pool.Clear(path)` drops every extra and removes the trust file (idempotent). `Pool.ClearOnceBaseTrusts(path)` calls it only when the base `ca.crt`, re-read from disk, contains the newest extra (the latest rotation's new CA, by certificate equality). The projected file can lag the API server by about a minute or more, so clearing earlier would lock the client out; until the base carries the new CA the extras stay and the next pass tries again. The base then alone trusts the API server.
- Where it runs: `caRun.complete` (the terminal step, after the client restarts and the settle delay) tries once, so the common case is cleared at once. Because a Completed operation is never reconciled again, a lagging `ca.crt`, a crash between Done and the clear, or a restarted container whose emptyDir still holds the file are handled by `controller.CATrustSync`, a runnable with `NeedLeaderElection() == false` on every replica. Every 10 s it lists CA-rotation operations (uncached): while one is under way it publishes that cluster's `rotation.yaml` Kubernetes CA certificate (certificate only, never the key); with none under way it calls `ClearOnceBaseTrusts`. Not clearing while a rotation is under way, plus the base check, make clearing safe: a pending rotation's CA is never in the base yet, and a rotation that starts again is republished by the leader on its next pass, long before Issuing.
- Follower: real. The webhook handlers (`internal/webhook/v1alpha1/override_webhook.go`, a client built from `mgr.GetConfig()`) and the caches run on every replica and use the config `kubetrust.Shared.Apply` rewrote in `cmd/waitapi.go`, whose pool on a follower held only the projected `ca.crt`. The sync publishes before Issuing, while the follower's old CA still works, so the follower is ready for the switch like the leader. RBAC is unchanged (the same ServiceAccount already lists operations and reads the bundle Secret).
- `Publish` no longer rewrites the file when nothing was added and the file exists.
- `make run` against a kubeconfig has no base file, so nothing is ever cleared there; it also never has extras beyond what it published itself.

## Amendment: the follower publishes on an event, not on a tick

With two controller replicas, at each CA switch the follower still gets `x509: unknown authority` for about 80 s, until its projected `ca.crt` refreshes. A 10 s poll does not publish "long before Issuing": Accepting to Issuing takes only 6-8 s, so Issuing can come before the next tick. Once the API server serves the new CA the follower cannot list operations to publish it.

- Decision: `CATrustSync` registers an event handler on the manager's TalosOperation informer (`mgr.GetCache().GetInformer`; informers run on every replica regardless of leader election) and requests a pass on every add or update of a CA rotation that is under way (`caRotationUnderway`). Passes run only in the runnable's own loop, which selects on the tick and a one-slot trigger channel (a non-blocking send), so the handler and the tick never run a pass concurrently. The 10 s tick stays as the fallback and does the clearing. If the informer cannot be obtained it logs and relies on the tick.
- `rotation.yaml` needs no extra trigger. `caRun.preflight` writes it to the bundle Secret before it sets the status to Accepting, and "under way" requires a non-empty stage, so the first event a follower acts on is that status update, after the Secret holds the keys. The pass reads the Secret uncached (`GetAPIReader`), so no Secret watch or cache is needed. The creation event itself (no status yet) is ignored.
- No fixed hold in Accepting was added. The publish now happens within milliseconds of the Accepting status, and the rotation then still applies Accepting to every machine and verifies it (the 6-8 s; AwaitingCredentials, ADR 0091, adds time when the operator is slow), so the race is against event latency, not against a tick. A fixed delay would only be a guess; a per-replica acknowledgement (a Lease per pod) would make it certain and is left for when hardware shows the event is still too late.

## Amendment (2026-10-29): the CNI is restarted first

With Cilium as the CNI, after the Kubernetes CA rotation the cilium agents and cilium-operator still held the old CA (`x509: certificate signed by unknown authority` against the API Service), went 0/1 Ready, and Cilium tainted both Nodes `node.cilium.io/agent-not-ready:NoSchedule`. The restarted CoreDNS pods stayed Pending on that taint, so the sequence (written for flannel, CoreDNS first) never reached the cilium restart. After the 5-minute bound the operation completed with DNS and the CNI broken. `kubectl -n kube-system rollout restart ds/cilium deploy/cilium-operator` fixed it in 12-16 s.

- Decision: the order is now the CNI first (the `cilium` DaemonSet, then the `cilium-operator` Deployment), then `kube-proxy`, `coredns`, `ichor-ui`; each waits for the one before to be available (supersedes the order in the "Ordering" bullet above; the flannel preset no longer exists, ADR 0099). RBAC: `cilium-operator` is added to the `get;patch` Deployment names in kube-system.
- Unmanaged CNI (`spec.cni.preset: unmanaged`, ADR 0103): Ichor does not know or touch the CNI, so no CNI workload is restarted (`kube-proxy`, `coredns` and `ichor-ui` still are). The completion message says the CNI was not restarted and that the operator should restart its pods if they report x509 errors.
- Outcome honesty: a workload not back at the bound still does not hold the rotation (the CAs are replaced and the bundle must be exported), but the result is no longer a clean success. The operation ends `Completed` with reason `RotatedClientsNotBack` (not `Rotated`) and the note naming what is not back; the `TalosCluster` condition `CAClientsNotRestarted` is set True (the same cluster-condition plus Warning Event vocabulary as `BundleBackupStale`), and the UI lists it under Needs attention. The next rotation whose clients come back sets it False. `Failed`/`Paused` were rejected: the rotation itself succeeded and a pause would block the unlock of the next one.

## Amendment (2026-10-30): the lease is handed over on a voluntary stop

`LeaderElectionReleaseOnCancel` stays off for the reason above: the API server may well be up while the process sits in `WaitForRestoreTails`, so a release there could let a second replica act mid-restore. But keeping the lease on every stop made each upgrade wait out the 120 s lease before the new controller issued ichor-ui's certificates (ADR 0105). The process now builds its own Lease lock (`cmd/leaderelection.go`, so it knows its identity) and, after `mgr.Start` returns, calls `releaseLeaseUnlessRestoring`: unless an etcd restore job is running (`controller.RestoreTailsRunning`) and only while the Lease's holder is still this process, it clears the holder with an update that fails if the Lease changed since it was read. A crash, a lost lease, or a stop that goes into the restore tail keeps the old behaviour (the lease runs out).
