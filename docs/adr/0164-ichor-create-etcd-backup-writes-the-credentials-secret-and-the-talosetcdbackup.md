# 0164. `ichor create etcd-backup` writes the credentials Secret and the TalosEtcdBackup

Status: Accepted · Date: 2026-11-15 · Design section: Backup, recovery, certificates · Touches: 0047, 0084, 0096

Decided: a new command `ichor create etcd-backup NAME --cluster C --s3-endpoint URL --s3-bucket B` writes the S3 credentials Secret in the controller's namespace and the `TalosEtcdBackup` that names it, with the operator's own kubeconfig; the credentials come from `ICHOR_S3_ACCESS_KEY_ID` and `ICHOR_S3_SECRET_ACCESS_KEY` (and optionally `ICHOR_S3_SESSION_TOKEN`), never from flags; the bucket is not created.

## Context

Nothing in Ichor created a `TalosEtcdBackup`: not `ichor init`, not the UI, not any command. `ichor backup etcd NAME` takes the name of one and only sets an annotation on it. The only route was a hand-written object, from a sample that was an empty `# TODO(user)` stub, plus a Secret in the controller's namespace (not the object's) with two required keys. The guides tell an operator to take an etcd snapshot before a CA rotation, which therefore could not be followed, and a rotation ran with no backup. Nothing in the rotation path enforces one; this decision does not either.

## Decision

- **A command, `ichor create etcd-backup`, not a flag on `ichor init`.** `create` already means "make the thing I need" (`create credentials`, `create talosconfig`, `create kubeconfig`, `create user`). A backup is wanted on clusters that exist already, adopted ones included, and `init` runs once, before the cluster has approved control planes to snapshot, so an `init` stanza would not reach them. An `init -f` key for a backup could call the same code later; it is not built now.
- **Written with the caller's kubeconfig, not through ichor-ui.** The two objects are ordinary resources and the API server's RBAC and the admission webhook decide, as for `ichor create user --bootstrap`. No new RPC, no proto change, and no credential passes through ichor-ui. The controller stays the only writer to Talos nodes, and `spec` is written once by the caller.
- **Credentials from the environment.** `ichor restore cluster` already reads `ICHOR_S3_ACCESS_KEY_ID`, `ICHOR_S3_SECRET_ACCESS_KEY` and `ICHOR_S3_SESSION_TOKEN` (falling back to the `AWS_*` names) and refuses them as flags so they stay out of `ps`. The new command reads the same variables, so one `export` serves both. A file was not added, as the bundle passphrase has one because it is generated and read back; an S3 key is issued by the store and lives in the operator's environment already. `--s3-ca-file` names a PEM file for the optional `ca.crt` key.
- **Ordering and clean-up.** The command checks the `TalosCluster` exists, then that neither object exists (it never overwrites a Secret or a backup), then creates the Secret, then the `TalosEtcdBackup`, and removes the Secret again, by UID, if the `TalosEtcdBackup` is refused. It applies the CRD's patterns and the webhook's `http://` rule itself, before anything is written, so a refusal costs nothing; the API server and webhook still refuse the same inputs from any other client.
- **The bucket stays outside Ichor.** The command does not create or check a bucket and says so. Automation stops at the Secret and the object: the bucket, its credentials and its lifecycle belong to the store. It warns, as the `StoreInCluster` condition does, when the endpoint is inside the cluster.
- The prefix defaults to the cluster's name, and the Secret to `NAME-s3`. The S3 credentials Secret key names move from the controller to `internal/etcdbackup`, so the command and the controller share them.
- `config/samples/ichor_v1alpha1_talosetcdbackup.yaml` becomes a working example and joins the samples that the API suite applies. It was the only sample with the placeholder.
- A guide, [Back up etcd to S3](../guides/etcd-backup.md), carries the procedure, and the pre-rotation advice in the CA-rotation and hardening pages names it, and says what `ichor snapshot etcd` is: plaintext, no setup, not an input to `ichor restore`.

## Consequences

- The operator still has to provide a bucket and credentials. This is the cost accepted: the setup is shorter, not gone.
- Setting the Secret reference and the destination still needs `update` on `talosetcdbackups/secret-references` (ADR 0096); an operator without it is refused by the webhook, after the Secret was written and removed again.
- Rotating the credentials is an edit of the Secret, not of this command, which refuses an existing one.
- Nothing yet makes a rotation wait for a backup. That would be a separate decision.
