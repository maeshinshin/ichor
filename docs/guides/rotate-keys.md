# Rotating the service-account and secretbox keys

`ichor cluster rotate-ca` replaces the Talos API and Kubernetes API certificate
authorities. Two more secrets are worth replacing after a compromise or on a
schedule, and `ichor cluster rotate-keys` does that (ADR 0121):

- the **service-account signing key**: the key that signs the tokens of every
  ServiceAccount, including the ones pods mount, and that kube-controller-manager
  signs legacy token Secrets with;
- the **secretbox key**: the key kube-apiserver encrypts Secrets with before it
  stores them in etcd.

The etcd CA and the aggregator CA are not rotated by either command.

## What it does

Both keys need a dual-key period: the old key keeps verifying or decrypting until
nothing depends on it. The operation therefore runs in stages, each verified on
every control plane before the next starts:

| Stage | Service-account key | Secretbox key |
|---|---|---|
| `Accepting` | the new public key is accepted for verification; the old key still signs | the new key is added as a second provider that only decrypts; the old key still encrypts |
| `Issuing` | the new key signs; the old public key is still accepted | the new key encrypts; the old one still decrypts |
| `Reencrypting` | | every Secret in the cluster is rewritten so it is stored under the new key; ends after a sweep that rewrote nothing |
| `SettlingTokens` | the old key is kept while pods' tokens are re-issued (90 minutes by default) and until no legacy token Secret still depends on it | |
| `Dropping` | the old public key is removed | the old key is removed |
| `Finalizing` | the secrets bundle in the cluster is replaced and the retired keys are recorded | |

A stage that does not apply to the keys you chose is skipped. Only control planes
carry these settings, so workers are not touched. Control planes are reconfigured
one at a time and Ichor waits for each kube-apiserver to restart and answer before
it goes to the next, so the Kubernetes API stays up on a cluster with more than one
control plane (with one, it is down briefly in each applying stage). No node
reboots, and your kubeconfig and talosconfig keep working: no CA changes.

## Running it

```
ichor cluster rotate-keys NAME
```

shows the plan and asks for confirmation (`--yes` skips it). The UI has the same
action, **Rotate keys**, in the cluster page's menu. Starting it needs the `start`
verb on `talosoperations/key-rotation`, held by `ichor-admin`.

Options:

- `--only service-account` or `--only secretbox` rotates one key.
- `--token-settle 2h` keeps the old service-account key longer. Pods' projected
  tokens live one hour by default and the kubelet re-requests them after 48
  minutes, so 90 minutes covers them; raise it if your workloads ask for longer
  lifetimes. **Less than 75 minutes is refused** (by the CLI, the UI and the
  admission webhook): a pod that still holds a token signed by the old key is
  refused by the API server the moment the key is dropped, and Ichor's own
  controller, the CNI and CoreDNS are such pods until the kubelet refreshes their
  tokens.
- `--ignore-legacy-tokens` drops the old key although Secrets of type
  `kubernetes.io/service-account-token` that it signed still exist. By default the
  operation waits and names them. Deleting the `token` key of such a Secret makes
  kube-controller-manager write a new one under the new key.
- `--follow` reports progress until it ends.

When the new service-account key signs, Ichor rolls its own controller and
ichor-ui Deployments once (a restart of the controller running the rotation: it
resumes by itself), and it does not drop the old key before they have rolled out.
The pods of other software that hold a token signed by the old key are refreshed
by the kubelet; the old key is kept for `--token-settle` for that.

Tokens kept **outside** the cluster, or requested with a lifetime longer than
`--token-settle`, cannot be found; they stop working when the old key is dropped.

Before anything is written, every control plane must answer, run exactly the
configuration Ichor renders for it, have a ready kube-apiserver, and every Node
must be Ready. The cluster must use `versionContract` v1.14 or later (the older
form keeps each key in one field that cannot hold two). A step that cannot be
verified pauses the operation (`Blocked` on the `KeyRotation` condition) and it
continues by itself when the cause is gone. Nothing is rolled back: every stage
keeps both keys working, so stopping anywhere is safe.

While it runs, the machine controller does not reconcile the control planes and
other disruptive operations are refused. The new keys are kept in the bundle Secret
(`keyrotation.yaml`) before any node is told to use them.

## Afterwards

1. `ichor cluster bundle-export NAME`. The bundle in the cluster is replaced and it
   now also holds the **retired keys** (`retired-keys.yaml`): the old secretbox
   secret, from which the key of backups sealed before the rotation derives, and the
   fingerprint of the old service-account key. The export writes the bundle and
   `retired-keys.yaml` next to it; keep both. Without the second file a backup sealed
   before the rotation cannot be opened from the bundle and the snapshot alone.
2. Defragment etcd on each control plane (`ichor node` action `etcd-defrag`): older
   etcd revisions, and the snapshots and backups taken before, are still encrypted
   under the old key until compaction and defragmentation drop them or you delete
   them.

New backups are sealed with the new key. A **snapshot taken before the rotation
finished cannot be restored** into the running cluster: its Secrets, the bundle
among them, are under keys the control planes no longer use. Ichor refuses it and
names the operation. Total-loss recovery (`ichor restore --recover-cluster`) with
the exported bundle and `retired-keys.yaml` can open an older snapshot, but the Secrets in
it are under the retired secretbox key, which the new bundle's configuration no longer
carries: to run a cluster from such a snapshot use the bundle that matches it.
