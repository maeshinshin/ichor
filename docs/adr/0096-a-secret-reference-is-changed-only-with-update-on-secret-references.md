# 0096. A Secret reference is set or changed only with update on the resource's secret-references

Status: Accepted · Date: 2026-10-27 · Design section: Security, RBAC · Extends: 0094

Decided: any create or update that sets or changes a field naming a Secret needs the verb `update` on the subresource `secret-references` of that resource (`talosclusters/secret-references`, `talosetcdbackups/secret-references`), asked of the requesting user with a SubjectAccessReview in the validating webhook that already gates `spec.authentication`; the controller and the GitOps engines are exempt as there; writing the same value again is not gated and neither is removing a reference; `ichor-admin` holds the right, `ichor-operator` and `ichor-viewer` do not, and the ichor-ui ServiceAccount does not need it

## Context

ADR 0094 gives `ichor-operator` `update` and `patch` on `talosclusters` and `talosmachines` and gates `spec.authentication` alone. But several spec fields name a Secret that the controller (or the UI backend) then reads with its own, broader privileges, in its own namespace. An operator who may edit a cluster could repoint such a field at any Secret in that namespace and make Ichor use it: for a registry's `authSecretRef` the controller copies the Secret's password-shaped keys into the node's machine config, which the operator can then read back. Editing the object was meant to be the operator's right; choosing which Secret Ichor reads was not.

## The fields

Four fields name a Secret. Nothing on `TalosMachine`, `TalosConfigPatch`, `TalosStorage`, `TalosOperation`, `IchorUser` or `IchorInstallation` names a Secret.

| Field | Read by | Why it is sensitive |
| --- | --- | --- |
| `TalosCluster.spec.secretsRef` | controller, UI backend (credentials download), `ichor` CLI | Names the Secret holding the Talos, Kubernetes and etcd CA bundle. Repointing it makes the controller sign node configs and the etcd backup key with another Secret, or reads a bundle the caller should not see back through the credentials download. |
| `TalosCluster.spec.registries[].authSecretRef` | controller, at render time | Its password, auth and identityToken keys are written into the node's machine config. An operator could exfiltrate any Secret of the Ichor namespace that has such keys into a config they can read. |
| `TalosCluster.spec.gitops.repository.tokenSecretRef` | UI backend, in its own namespace | The token pushes branches and opens pull requests. Repointing it makes the backend act with another Secret's token (a Git host token chosen by the editor), or with a different key of the same Secret. |
| `TalosEtcdBackup.spec.destination.s3.credentialsSecretRef` | controller, in its own namespace | The S3 credentials the snapshots are written and pruned with. Repointing it sends every snapshot, and the prune, to credentials of the editor's choosing. |

## Decision

**The right is `update` on `<resource>/secret-references`.** The same shape as ADR 0094's `talosclusters/authentication`: a subresource that exists only as an RBAC name. The webhook (`vauthentication-v1alpha1.kb.io`, which already receives `talosclusters` create and update) runs the check first, and a new rule `vsecretrefs-v1alpha1.kb.io` sends `talosetcdbackups` create and update to the same handler. The check is a SubjectAccessReview as the requesting user, so a hand-written CR obeys it as the CLI and UI do.

**What counts as a change.** On create, a reference that is set. On update, a reference whose value differs from the stored one (for the token, name or key). For `registries[]`, entries are matched by `host`: an `authSecretRef` that is new or names another Secret is a change. A removed reference is not a change: it only stops Ichor using a Secret. A write that leaves every reference as it was, the UI's upgrade write for one, is never asked.

**Who is exempt.** The controller's identity and the GitOps engines (`ExemptUsers`), exactly as for the authentication gate: the engines apply what a person reviewed in Git, and the controller never changes `spec`.

**Roles.** `ichor-admin` gets `update` on `talosclusters/secret-references` and `talosetcdbackups/secret-references` (kustomize role and chart template). `ichor-operator` does not. The ichor-ui ServiceAccount does not: with OIDC on the backend writes as the user; with it off the backend's writes (upgrades, machine approval, patches) leave the references as they were, and the webhook does not ask for an unchanged value. A cluster created by `ichor init` sets `secretsRef`, so it needs an administrator, as creating the bundle's Secret already does.

## Consequences

An operator can no longer change where Ichor reads credentials from; setting one up (a new registry credential, an S3 bucket) is an administrator's step, and the operator keeps editing everything else. The check adds a SubjectAccessReview to a write only when a reference is touched. GitOps users change a reference through Git, reviewed there. A reference to a Secret the author may not read is still possible for an administrator; narrowing that (an allow-list of Secret names) is left out.
