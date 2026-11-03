# 0111. An upgrade prunes what the previous run installed and the new manifest drops

Status: Accepted · Date: 2026-11-03 · Design section: Upgrades (Ichor), Trust · Touches: 0058, 0059, 0105, 0108, 0109

Decided: after the new manifest is applied and checked, the installer deletes the objects that the last successful run recorded and the new manifest no longer ships, and the ClusterRole that ADR 0109 renamed when an earlier build still holds it. It deletes an object only when the object carries `ichor.maesh.dev/installed-by=ichor-installer` (for the renamed ClusterRole from before that label, `app.kubernetes.io/name=ichor` instead), is of a kind the installer may delete, and is cluster-scoped or in the installer's namespace. CRDs, Namespaces, Secrets, PriorityClasses, CiliumNetworkPolicies and unlabelled objects are never deleted. This is how a later version renames or drops an object.

## Context

The installer (ADR 0059) applies the target manifest with server-side apply under its own field manager, and never deletes anything. An object that a version stops shipping stays in the cluster. ADR 0109 renamed the safety-check override's ClusterRole from `ichor-ichor-break-glass-role` to `ichor-ichor-override-role`; the upgrade to that build left the old ClusterRole in place, with its `app.kubernetes.io/name=ichor` label and the rule that granted the old verb. The user asked for a mechanism that removes such objects, so that future renames migrate cleanly. Ichor is not released yet, so the mechanism can start from the current design.

## Decision

**The mark.** Every object the installer applies carries `ichor.maesh.dev/installed-by=ichor-installer` (the installer's field manager). The installer stamps it itself, so the bundle files do not need changing and the Helm chart's objects never carry it.

**The inventory.** Each successful run writes a ConfigMap `ichor-installer-inventory` in its own namespace, listing the apiVersion, kind, namespace and name of every object it applied. The next run reads the previous inventory before it prunes. The installer's Role may create and delete ConfigMaps but not update them, so the write is a delete followed by a create. A recorded inventory that cannot be parsed is ignored (logged). A failed write is logged and the run succeeds: the next run then prunes less, never more.

**The candidates.** An inventory object is a candidate when the new bundle does not ship it, its kind is in the prunable set (below), it is cluster-scoped or in the installer's namespace, and its name does not start with `ichor-installer` (the installer's own RBAC, which the run needs until its last step). The renamed ClusterRole of ADR 0109 is a candidate whenever it is present and the bundle does not ship its old name.

**The owner check.** At delete time each candidate is read again. It is deleted only if it carries the mark, or, for the renamed ClusterRole, the app name label. Anything else is kept and the installer logs the kept object.

**The prunable kinds.** ClusterRole, ClusterRoleBinding, Role, RoleBinding, ServiceAccount, Service, Deployment, MutatingWebhookConfiguration, ValidatingWebhookConfiguration, ValidatingAdmissionPolicy and ValidatingAdmissionPolicyBinding. Each is one the installer's role already holds delete on; a test fails if that stops being true.

**Never pruned:**
- CustomResourceDefinitions: deleting one deletes every custom resource of its kind.
- Namespaces, and Secrets (state: the CA, backup keys, users).
- PriorityClasses: the installer holds no delete on them.
- CiliumNetworkPolicies: whether the bundle includes them depends on the detected CNI, so their absence says nothing.
- PersistentVolumeClaims and anything else not in the list above.
- Any object without the mark, and any object in another namespace (the Cilium Job's objects in kube-system stay as they are).

**Order and rollback.** The prune is the last stage, after the CRDs, controller, UI and network policies are applied and checked. The objects it will delete are snapshotted first (stage 3 of the run's snapshot), so a failed prune rolls the whole upgrade back, as any failed step does.

**RBAC.** No change to the installer's role. Discovery goes through the inventory, so the installer does not need `list` on the prunable kinds; it reads them with `get`, which it already holds.

**Renames.** `renamedObjects` in the installer lists renames whose old objects a build before this ADR may still hold. It has one entry, the ADR 0109 role. A rename made by a build with this ADR needs no entry: the previous build recorded the old object in its inventory, and the prune removes it. The list only grows with renames already shipped.

**The plan.** `ichor upgrade ichor` prints a NOTICE naming the renamed objects still present, since the CLI can see those without the new manifest. The full prune set is not shown before the upgrade: the CLI does not hold the target manifest, so the installer logs each deletion instead.

## Consequences

- A rename of an object that a build with this ADR installed is removed by the next upgrade, with no code change beyond the rename.
- The first upgrade to a build with this ADR has no inventory. Only `renamedObjects` can be pruned by that first run; the objects that run's inventory records are candidates from the second upgrade on.
- Helm-managed installs are not touched by the inventory (their objects lack the mark). Helm removes objects its own release record names and the chart no longer renders, so the ADR 0109 role also leaves a Helm-managed cluster on `helm upgrade`.
- An object applied by an older build and then relabelled or re-created by a person is kept: the mark is the proof, and a person removing it is taken as the answer.
- The controller never prunes; the installer Job is still the only process that deletes Ichor's manifests (ADR 0058).
