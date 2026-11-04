# 0121. The service-account key and the secretbox key rotate in a dual-key period, and the backup key is versioned

Status: Accepted · Date: 2026-11-04 · Design section: Backup, recovery, certificates · Touches: 0047, 0063, 0089, 0091, 0094, 0108

Decided: a new `TalosOperation` kind `key-rotation` (`spec.keyRotation.targets`: `serviceAccount`, `secretbox`, or both) rotates the Kubernetes service-account signing key and the kube-apiserver secretbox (encryption-at-rest) key through a status-held state machine of its own, `Accepting`, `Issuing`, `Reencrypting`, `SettlingTokens`, `Dropping`, `Finalizing`, `Done`: the new key is first only verified or decrypted with on every control plane, then made primary, every Secret is rewritten under it (by the controller, through the admin kubeconfig a Talos node issues, so Ichor's own RBAC does not grow), the old signing key is kept until bound tokens have been re-issued (default 90 minutes, `spec.keyRotation.tokenSettle`) and no legacy token Secret still depends on it, and only then is the old key dropped. The new keys are saved in the bundle Secret (`keyrotation.yaml`) before any node is told, and `secrets.yaml` is replaced last. The etcd backup key is versioned: the old secretbox secret is kept in a new bundle Secret key `retired-keys.yaml` (and in a file of the same name next to the exported bundle), `etcdbackup.Key` gains `Retired` passphrases derived from it, and `Decrypt` tries them, so backups sealed before a rotation stay readable. A restore of a snapshot older than a key rotation is refused, as for a CA rotation. It needs `versionContract` v1.14 or later.

## Context

ADR 0108 section 6 deferred these two keys: "Kubernetes needs a dual-key period for the last two (the old key must keep verifying or decrypting until every token and Secret is re-written), which is a multi-stage operation with a re-encryption pass, ... and the backup key (section 4) derives from the secretbox secret, which would have to be versioned first." Until now "rotate after compromise" did not cover either key. The user decided on 2026-11-04 that it ships in v0.1.0.

What the docs and the code say (Talos machinery v1.14.5 in `go.mod`, Kubernetes 1.36 `k8s.io/apiserver` v0.36.2):

- **Service-account key.** Talos v1.14 has the document `KubeServiceAccountConfig` ([reference](https://docs.siderolabs.com/talos/v1.14/reference/configuration/kubernetes/kubeserviceaccountconfig)): `issuer.privateKey` signs, `accepted.publicKeys` are "used by the Kubernetes API server to verify the service account tokens ... This might be used for service account rotation". `AcceptedKeys()` is the issuing key's public half followed by `accepted.publicKeys` (machinery `config/types/k8s/service_account.go`); the issuing key is also what kube-controller-manager signs legacy tokens with. The legacy field `cluster.serviceAccount` holds one key and the two forms conflict (`V1Alpha1ConflictValidate`), so only the multi-document form can hold two keys.
- **Secretbox key.** The document `KubeEtcdEncryptionConfig` ([reference](https://docs.siderolabs.com/talos/v1.14/reference/configuration/kubernetes/kubeetcdencryptionconfig)) carries the kube-apiserver `EncryptionConfiguration` as is. For a bundle machinery generates it as one provider `secretbox` with the key `key2` = `Secrets.SecretboxEncryptionSecret` (`config/generate/kubernetes.go`), and under the legacy contract the single field `cluster.secretboxEncryptionSecret`. The Kubernetes procedure ([encrypt data at rest, "Rotate a decryption key"](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/)) is: add the new key as the second entry and restart **all** kube-apiservers; make it the first entry and restart all; rewrite every Secret (`kubectl get secrets --all-namespaces -o json | kubectl replace -f -`); remove the old key. The first provider/key encrypts, every listed one decrypts.
- **Key names need not differ.** `k8s.io/apiserver` `pkg/storage/value/transformer.go` (`prefixTransformers.TransformFromStorage`) tries every transformer whose prefix matches and does not stop at one that fails: "It is valid to have overlapping prefixes when the same encryption provider is specified multiple times but with different keys (the first provider is being rotated to and some later provider is being rotated away from)". `validation_encryption.go` checks no uniqueness of secretbox key names. A key's name is the prefix of what it wrote (`k8s:enc:secretbox:v1:<name>:`), so the new key must carry the name the finished configuration gives it, `key2`; a different name during the rotation would make every Secret written meanwhile unreadable afterwards.
- **A no-op update re-encrypts a stale Secret.** `etcd3.Store.GuaranteedUpdate` skips a write only when the object is not `stale`, and `stale` is true when a transformer other than the first decrypted it (`stale || i != 0`). Rewriting every Secret with its own content therefore moves exactly the ones under the old key, and a sweep that changes no `resourceVersion` proves none is left.
- **Bound tokens roll by themselves, legacy ones do not.** The kubelet re-requests a projected token "if it is older than 80% of its total time-to-live (TTL), or if the token is older than 24 hours"; the default TTL is one hour ([service account token projection](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/)). A token Secret of type `kubernetes.io/service-account-token` made by hand never expires and is signed once. A `subPath` mount of a projected token is never refreshed.
- **The backup key** (ADR 0108 section 4): `etcdbackup.DeriveKey` is HKDF-SHA256 over `Secrets.SecretboxEncryptionSecret`, info `ichor.maesh.dev/etcd-backup/v2`, salt the cluster ID.

## Decision

### 1. The operation

`TalosOperation.spec.kind: key-rotation`, with `spec.keyRotation`:

| Field | Meaning |
|---|---|
| `targets` | A non-empty set of `serviceAccount`, `secretbox`. Both is the normal request; one key can be exposed without the other. |
| `tokenSettle` | How long to wait, once the new service-account key signs, before the old one is dropped. Default 90 minutes (a one-hour token is re-requested after 48 minutes; the rest is margin). Raise it when workloads request longer-lived projected tokens. |
| `ignoreLegacyTokens` | Go on although Secrets of type `kubernetes.io/service-account-token` signed by the old key still exist (section 4). Default false. |

All three are immutable after creation. Status is `status.keyRotation`: `stage`, `keys[]` (per target the previous and next key's public identifier, which one signs or encrypts and which are accepted in the stage; a public-key fingerprint for the service-account key, a salted hash prefix for the secretbox secret, so no key material), `restart` (the control plane being restarted and the apiserver container it ran), and `reencryption` (sweeps, secrets rewritten in the last sweep). The kind needs the RBAC verb `start` on the subresource `talosoperations/key-rotation`, like `ca-rotation`, so the two can be granted separately: a CA rotation can lock operators out, a key rotation cannot.

A new kind, not a target of `ca-rotation`: its stages, gates (no credential hand-off, but a settling period and a re-encryption sweep), saved material (keys, not CAs), status shape and failure modes are different, and sharing `status.caRotation` would make every consumer of that field (the CLI's credential hand-off, the UI, the CA trust sync, ADR 0091's gate) branch on what it is looking at. The cost is a second state machine that borrows the first's helpers rather than a shared one.

### 2. Stages and the rendered configuration

Only control planes carry either document (both are control-plane-only documents in machinery), so only they are visited, one at a time; a worker is never applied to. `pkg/render` gets `SystemInputs.KeyRotation{Stage, New}`, set by the executor only, which edits the two documents of the already generated base configuration (below every `TalosConfigPatch` layer, like the accepted CAs):

| Stage | Service-account key | Secretbox key |
|---|---|---|
| (before) | old signs; old public verifies | provider `[old]` |
| `Accepting` | old signs; `accepted.publicKeys` = `[new public]` | providers `[old], [new]` (new decrypts only) |
| `Issuing` | new signs; `accepted.publicKeys` = `[old public]` | providers `[new], [old]` (new encrypts) |
| `Reencrypting`, `SettlingTokens` | as `Issuing` | as `Issuing` |
| `Dropping`, `Finalizing` | new signs; nothing accepted | provider `[new]` |

`Dropping` is byte for byte what a bundle holding the new keys renders, so ending the rotation leaves the machine controller no drift (a test pins this, as for ADR 0063). The secretbox key is a second provider, not a second key of the first, because the Kubernetes source names exactly that shape as the supported overlap. Each stage is applied with `ApplyNoReboot`, to one control plane at a time: Ichor records the control plane and its kube-apiserver container (`APIServerInstance`), applies, and waits for another container and `/readyz` (`APIServerReady`) before the next control plane and, after the last, before the stage is verified, so a mixed state never has fewer than two accepting apiservers' worth of safety and a three-control-plane cluster keeps a quorum of API servers. The previous stage is verified on every control plane before the next stage starts: no apiserver signs or encrypts with the new key before every apiserver can verify and decrypt with it. Every Kubernetes Node must be Ready at every verification.

The preflight refuses to start (nothing is written, not even a key) unless every approved machine answers, runs exactly the configuration Ichor renders for it, every Node is Ready, `versionContract` is v1.14 or later (the single-field legacy form cannot hold two keys; the message names the contract to raise), the bundle has a secretbox secret when `secretbox` is a target, and rendering the first stage changes the configuration of every control plane (a `TalosConfigPatch` that overrides either document would otherwise make the stages no-ops that read as verified).

### 3. Re-encryption (`Reencrypting`)

Who: the controller, through the admin kubeconfig a Talos node issues (`restConfig`, the way `NodesReady` and the root-CA check already reach the cluster). No Ichor ServiceAccount gains a right on Secrets; the credential exists only for the sweep. How: list every Secret page by page and `Update` it with its own content (the `kubectl replace` of the Kubernetes procedure). A `NotFound` is skipped, a `Conflict` leaves that Secret to the next sweep. A sweep counts the Secrets whose `resourceVersion` the update changed; the stage ends after a sweep that changed none, which is the proof above. `Dropping` never starts before that. Each reconcile runs at most one sweep within a bounded time and the status carries the counts, so a killed controller resumes with a new sweep (idempotent). Rejected: a Job with a ServiceAccount allowed to update every Secret (a standing cluster-wide credential for a rare task), the `kube-storage-version-migrator` (another component, CRDs and RBAC in the user's cluster, and Ichor installs nothing it does not own, ADR 0101), reading etcd to check the stored prefix (Talos exposes no key read; the prefix is the same for both keys anyway, see Context).

### 4. Service-account tokens (`SettlingTokens`)

The stage waits `tokenSettle`, timed from the `KeyRotation` condition's transition time so it survives a restart, with the Nodes required Ready. Then it counts the Secrets of type `kubernetes.io/service-account-token` whose token verifies with the old public key (RS256 and ES256 signature checks with the standard library over the three JWT parts; no token is ever stored or logged). If any exists and `ignoreLegacyTokens` is false the operation pauses (`Blocked`, reason `LegacyTokens`, the message names up to five `namespace/name`) and continues by itself when the Secrets are gone or re-issued (deleting the `token` key of such a Secret makes kube-controller-manager write a new one under the new key; Ichor does not do that for the user). Tokens that live outside the cluster, or that a client asked for with a long lifetime, cannot be seen; `tokenSettle` and the docs say so.

### 5. The bundle and the backup key

Before any node is told, the executor writes the new keys into the bundle Secret under `keyrotation.yaml` with an `Update` that fails if it exists (an earlier attempt's file is adopted), so a crash never leaves nodes using a key nobody holds. `Finalizing` replaces `secrets.yaml` (new service-account key, new secretbox secret), appends an entry to `retired-keys.yaml`, and removes `keyrotation.yaml` in one `Update` that fails if the Secret changed; it raises `BundleBackupStale` (ADR 0063) and verifies every machine against the replaced bundle.

`retired-keys.yaml` is a list of `{retiredAt, secretbox?, serviceAccount?}`: the retired secretbox secret, and the fingerprint of the retired service-account public key (so a stale local copy is recognised without keeping that key). Why not inside `secrets.yaml`: `talosctl` and `secrets.Bundle` own that format, ADR 0063's `replaceBundle` and the CLI re-marshal it from the machinery type and would silently drop an extra field, and ADR 0127 is about to encrypt it. A sibling file next to the exported bundle (`ichor cluster bundle-export` writes it, `ichor restore` reads it from the directory of `--secrets-bundle`) carries it the same way; it is key material and gets the same 0600 atomic write. The export fingerprint that clears `BundleBackupStale` is unchanged for a cluster that never rotated keys and folds in the retired list otherwise, so an export from before the rotation does not clear the condition.

`etcdbackup.Key` gains `Retired []string`: `DeriveKey(bundle, retired...)` derives one v2 passphrase per retired secretbox secret with the same HKDF info and salt (the format does not change), newest first; `Secrets()` and `Decrypt` try Current, Legacy, then Retired; new backups are sealed with Current as before. The backup controller, the restore executor and `ichor restore` pass the retired secrets they load. A backup sealed before a rotation therefore verifies and decrypts after it.

### 6. Restore across a key rotation

A snapshot taken before a key rotation finished holds Secrets, among them the bundle Secret, under keys the nodes no longer configure; restoring it would leave the controller unable to read its own bundle. The restore executor and the UI refuse such a snapshot the way `noRotationSince` refuses one older than a CA rotation: a snapshot taken before a `key-rotation` operation of the cluster finished is refused, with the operation named. The retired list is for reading old backups (verification, offline inspection, `ichor restore` with a bundle and a snapshot of the same era), not for putting them back under a different configuration.

### 7. Quiescing, failure, pause and the client surfaces

- While a rotation is under way (stage set, not `Completed`) the machine controller sets `Ready=Unknown/KeyRotationInProgress` and does nothing else; the disruptive-work gates refuse other operations and removals (`ClusterKeyRotating`); the `TalosOperation` carries a finalizer (`ichor.maesh.dev/key-rotation`), removed at `Done`, because the stage is the only record of which keys the nodes accept.
- A step that cannot be verified never advances: the operation is `Paused` with a `KeyRotation` condition `False`, reason `Blocked`, and tried again every `operationRequeueInterval` (each step is idempotent). `spec.paused` stops it between stages like any operation. There is no rollback: every stage keeps both keys working, so stopping anywhere is safe, and a stage is re-applied rather than undone. Abandoning a rotation by hand means removing the finalizer, knowing the nodes are in the stage status last showed and `keyrotation.yaml` is still in the Secret.
- Messages go through the same scrubber as `caRun`; the leak test covers the new keys.
- CLI: `ichor cluster rotate-keys NAME [--only service-account|secretbox] [--token-settle 90m] [--ignore-legacy-tokens] [--follow]`, through `OperationService` like `rotate-ca`. UI: a "Rotate keys" action beside "Rotate CAs" and the stage on the operation page. The completion message and the guide tell the operator to export the bundle (`BundleBackupStale`) and to defragment etcd on each control plane (`ichor node` action `etcd-defrag`): old etcd revisions and snapshots can still hold data under the old key until compaction and defragmentation drop them, and Ichor does not do that for them.

## Alternatives rejected

- **One `ca-rotation` operation with extra targets.** See section 1.
- **A new key name per rotation, kept in the bundle.** It would need a field `secrets.Bundle` does not have; the identical name works because the apiserver tries overlapping prefixes (Context).
- **Dropping the old secretbox key the moment the sweep ends, without the mixed stage in Accepting.** The Kubernetes procedure's first restart exists so no apiserver writes under a key another cannot read.
- **Keeping the old secretbox key configured as a decrypt-only provider for restores.** It would make "rotate after compromise" mean "the compromised key still decrypts"; restores across a rotation are refused instead (section 6).
- **A timer only for the service-account key.** The legacy-token check is the part a timer cannot cover, and it is cheap.

## Risks and limits

- **Not run on hardware.** The sequence follows the Kubernetes procedure and Talos's documented fields. What needs a real cluster: that `ApplyNoReboot` of `KubeServiceAccountConfig` and `KubeEtcdEncryptionConfig` restarts kube-apiserver and kube-controller-manager and does not need a reboot (`pkg/fieldmeta` has no rule for either document, so they keep the reboot-required default for the machine controller, which does not apply them), that two providers with the same key name read and write as the source says, that a no-op update rewrites a stale Secret on 1.36, and the time a sweep takes. Steps are in the ADR's hardware section below.
- **Outside tokens.** A token copied out of the cluster, or asked for with a lifetime longer than `tokenSettle`, stops working when the old key is dropped. The operation cannot find them.
- **Old data stays encrypted under the old key** in etcd history and in the snapshots and backups taken before; the retired key is kept to read the latter. Deleting old backups and defragmenting is the operator's.
- **The rewrite touches every Secret in the cluster** (new `resourceVersion`, watch events for every Secret watcher). That is what the Kubernetes procedure does.
- **Contract.** A cluster on a contract older than v1.14 cannot be rotated until the contract is raised, which is itself a configuration change.
- ADR 0127's encryption of the local bundle must cover `retired-keys.yaml` too.

## Hardware verification (later)

1. Cluster with 3 control planes and 1 worker, contract v1.14, a few hundred Secrets, one hand-made `kubernetes.io/service-account-token` Secret, and a TalosEtcdBackup with a first snapshot taken before the rotation.
2. `ichor cluster rotate-keys NAME --token-settle 5m`: expect `Accepting` to restart kube-apiserver on one control plane at a time with `/readyz` between; after it, from a control plane `talosctl read /system/secrets/kubernetes/kube-apiserver/encryptionconfig.yaml` (or the equivalent) shows two providers, old first; and `talosctl get` the service-account document shows the accepted key.
3. After `Issuing`, create a Secret, read it back, and check that a pod restarted now still gets a token; `kubectl create token` works.
4. `Reencrypting`: the status counts fall to zero in the last sweep; before `Dropping`, delete the old provider by hand on none (no manual change).
5. The legacy token Secret blocks `Dropping` with `LegacyTokens`; delete its `token` key and see the controller-manager write a new one and the operation continue; repeat with `--ignore-legacy-tokens`.
6. After `Done`: all Secrets readable, workloads running, Nodes Ready; a new backup is sealed with the new key; the snapshot from step 1 still decrypts (`ichor restore --verify` or the restore plan) and a restore of it is refused naming the operation; `ichor cluster bundle-export` writes `retired-keys.yaml` and clears `BundleBackupStale`.
7. Kill the controller during each stage once and check it resumes; take one control plane down before `Accepting` and check the preflight refuses.
