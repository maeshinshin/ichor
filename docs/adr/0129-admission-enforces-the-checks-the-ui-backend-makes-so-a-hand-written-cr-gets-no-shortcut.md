# 0129. Admission enforces the checks the UI backend makes, so a hand-written CR gets no shortcut

Status: Accepted · Date: 2026-11-05 · Design section: Trust, Webhooks, Cilium · Builds on: 0094, 0099, 0108, 0091

Decided: Ichor's rule is that the UI and the CLI change the world by writing CRs, and that a rule the UI backend enforces must also hold for a CR written with `kubectl` (ADR 0094 decision on destructive starts, ADR 0108). An audit found four places where it did not. (1) The UI, `ichor apply cni-values` and `ichor adopt` run `cilium.ValidateValues` on `spec.cni.cilium.values`; the TalosCluster webhook did not, and the controller only rejects owned keys at render time, so a built-in `operator` could `kubectl patch` values the UI refuses (schema violations, unknown keys, a value of a key Ichor owns, an oversized layer) and the controller would hand them to Helm. The TalosCluster webhook now runs the part of `ValidateValues` that needs no knowledge outside the object, on create when values are set and on update when `spec.cni.cilium.values`, `spec.cni.kubeProxy` or `spec.cni.tunnel` changes. The three guards that need the cluster's machine configuration or the live cluster (`bpf.masquerade` against `forwardKubeDNSToHost`, KubePrism in replaced mode, Gateway API CRDs) stay in the UI and the CLI. (2) `guardGrant` and `guardLastAdmin` lived only in `UserService`; a new `IchorUser` validating webhook applies both to every write of an `IchorUser`. (3) and (4) are documentation: the credentials hand-off annotation is an attestation, and `spec.paused` has no effect on an etcd restore that has started.

## Context

The audit compared every check in `internal/uiapi`, `internal/cli` and `pkg` with what a validating webhook enforces. Most rules already live in webhooks (the override verb, the `start` verbs, the version checks, the controller's own identity). Four did not:

1. **Cilium values.** `pkg/cni/cilium/validate.go` documents itself as "shared by the webhook, the Connect service and the controller" (ADR 0099 decision 4). Only the Connect service (`internal/uiapi/cni_service.go`: `ValidateCiliumValues`, `SetCiliumValues`, `StartCniUpgrade`) and `ichor adopt` (`internal/cli/adopt_run.go`) call it. The webhook has no call, and `internal/controller` has none either: the render path rejects only Ichor-owned keys (`Spec.CheckOwned`, `pkg/cni/cilium/effective.go`). Helm validates against the chart's `values.schema.json` when the Job runs, so a schema violation fails the Job late, but an unknown key (the schema has no `additionalProperties: false`, ADR 0099) or a value that is legal and harmful (`bpf.masquerade` with `forwardKubeDNSToHost: true`, which breaks CoreDNS) is applied.
2. **`IchorUser`.** `guardGrant` (a user may add only the roles they hold; admin may add any; ADR 0108) and `guardLastAdmin` (the last enabled admin cannot be deleted, disabled or demoted; ADR 0094) are Go code in `UserService`. Kubernetes RBAC alone decides a `kubectl` write, so anyone allowed to `update` `ichorusers` could grant themselves `admin` or lock everyone out. No `IchorUser` webhook existed.
3. **CA-rotation hand-off.** `ichor create credentials --acknowledge` and the UI verify that the new kubeconfig works (`internal/uiapi/operation_credentials.go`) before they write `ichor.maesh.dev/credentials-handoff`. The controller checks only that the annotation equals the token of the rotation's new CA fingerprints.
4. **`spec.paused` on an etcd restore.** `TalosOperationReconciler.Reconcile` hands an operation to `advanceEtcdRestore` as soon as `etcdRestoreStarted` is true, before it looks at `spec.paused`, because a started restore takes the Kubernetes API away and cannot wait for one.

## Decision

### 1. Cilium values in the TalosCluster webhook

`ValidateCreate` and `ValidateUpdate` of `TalosClusterCustomValidator` call `cilium.ValidateValues(values, cilium.ValidationContext{KubeProxy, Tunnel})` with the new object's `spec.cni.kubeProxy` (empty is `enabled`, as the UI reads it) and `spec.cni.tunnel`, and every other fact nil. A nil fact passes its guard (the existing contract of `ValidationContext`), so the webhook enforces exactly what needs nothing but the object:

- the chart schema, unknown keys, Ichor-owned keys and the size cap (`pkg/values`);
- that kubeProxy and tunnel are modes Ichor knows.

Rules:

- **Ratcheting.** On update the check runs only when `spec.cni.cilium.values`, `spec.cni.kubeProxy` or `spec.cni.tunnel` differs from the stored object (semantic equality). A stored cluster with values that predate a stricter check keeps accepting unrelated edits (versions, labels, finalizers). The check also does not run for an update by the controller, which never changes a spec (ADR 0116).
- **Not run** for `spec.cni.preset: unmanaged` (Ichor leaves the CNI alone; the UI refuses to touch its values too) and for a removed preset (refused earlier).
- **No override.** The UI has no override for Cilium values and gets none here: a refusal is lifted by fixing the values.
- A values field that is not a JSON object is refused.

What stays outside the webhook, because it needs a fact the webhook cannot read:

| Guard | Fact | Where it holds |
|---|---|---|
| `bpf.masquerade` needs `forwardKubeDNSToHost: false` | the effective machine config of a control plane (cluster, role, selector and machine patches, system layer) | UI, `ichor get|edit|diff|apply cni-values`, `ichor adopt` |
| replaced mode needs KubePrism on port 7445 | same | same |
| `gatewayAPI.enabled` needs the Gateway API CRDs | a live CRD lookup | UI and CLI; the install itself fails visibly without them |

The webhook does not render machine configuration: it would need the cluster's secrets bundle, every `TalosConfigPatch` and the registry credentials inside a 5 second admission call whose failure policy is `Fail`. ADR 0099 decision 4 says the controller "re-checks live before every Job"; the controller does not call `ValidateValues` today. That check is the one that closes the first row for `kubectl`. It is a follow-up in `internal/controller` (the CNI Helm Job path), recorded in the ADR 0099 amendment below and not done here.

The comment of `ValidateValues` is corrected: it is shared by the webhook (static part), the Connect service and the CLI; the controller is the planned fourth caller.

### 2. An `IchorUser` webhook

A validating webhook on `ichorusers` (create, update, delete; `failurePolicy: Fail`, 5 s, like the other validating webhooks) applies two rules to the authenticated user of the request (`req.UserInfo`). Both mirror the UI code exactly, and the UI keeps its own checks (defence in depth, and a better message).

**Grant** (`guardGrant`). It applies to an Ichor-sourced identity: a username that starts with `ichor:`. Only Ichor's own issuer can produce one; the external IdP's prefix may not equal or start with it and the apiserver blocks `system:` (ADR 0094). The requester is admin-equivalent if its groups contain `ichor:admin`, and then anything is allowed. Otherwise each role in `spec.roles` that was not in the stored object's roles (all of them on create) must be one of the requester's groups `ichor:<role>`. Other identities are not limited, as in the UI: an external identity has no Ichor roles and Kubernetes RBAC stays its only gate. The ichor-ui ServiceAccount, the controller and the override certificate are never `ichor:`-prefixed. The UI backend writes as the signed-in user (impersonation), so its writes arrive as `ichor:<name>` with that user's groups and get the same answer twice.

**Last admin** (`guardLastAdmin`). An update that turns an enabled admin (not disabled, role `admin`) into something else (disabled, or without `admin`) and a delete of an enabled admin are refused when no other `IchorUser` in the namespace is an enabled admin. The others are listed with the uncached reader. Exempt, and so the recovery path of ADR 0094: any user whose name starts with `system:` or who is in `system:masters` (the secrets-bundle `admin` certificate that `ichor init` writes into the kubeconfig has `O=system:masters`), which also covers the namespace controller deleting the namespace's objects on uninstall and the garbage collector. An object that already has a `deletionTimestamp` is not guarded. It stays what ADR 0094 called it, a guard against a slip and not a security boundary: `system:masters` or a `cluster-admin` binding can still remove the last admin, by design.

The two kinds of identity the guard applies to are thus `ichor:*` users and external OIDC users (`oidc:*`, or whatever prefix the site chose); only the grant rule is limited to `ichor:*`.

### 3. The credentials hand-off annotation is an attestation

No new mechanism. The annotation `ichor.maesh.dev/credentials-handoff` is not verified by the webhook or the controller beyond matching the token of the new CA fingerprints. Setting it by hand with `kubectl annotate` states "I hold a working kubeconfig and talosconfig for the new CAs". If that is false the rotation proceeds to Issuing, and the operator's old kubeconfig and talosconfig stop working once the old CAs drop; only a copy of the secrets bundle (`ichor create credentials --bundle`, or the bundle Secret for someone who can read it) makes new ones. `ichor create credentials --acknowledge` and the UI download verify the new kubeconfig against the cluster first and only then write the annotation. The guide (`docs/guides/hardening.md`, CA rotation) and the field's comment in `api/v1alpha1/talosoperation_types.go` now say so. Verifying inside the webhook would need the new CA's client key and a network path to the cluster from the webhook; the attestation is one `update` on `talosoperations`, and starting a CA rotation already needs the `start` verb on `talosoperations/ca-rotation`, held by `ichor-admin`.

### 4. `spec.paused` and a started etcd restore

`spec.paused: true` does not stop an etcd restore that has started (`status.etcdRestore` has begun the stage that takes the API away): the controller ignores it, because pausing would leave the API server stopped with nobody to bring it back. The comment of `TalosOperationSpec.Paused` now says it. This is a documentation change; the field is unchanged.

## Alternatives rejected

- **Render the machine config in the webhook to evaluate the hostDNS and KubePrism guards.** Needs secrets, patches and registry credentials in a 5 s fail-closed call and a second copy of `talosFacts`. The controller, which already renders, is the right place for the live check.
- **Fail the webhook on every unrelated update when stored values are invalid.** A stricter check shipped later would block version bumps and the automatic patcher's writes on clusters that were valid when stored. Ratcheting avoids that.
- **Make `guardLastAdmin` apply to every identity, system ones included.** It would stall namespace deletion on uninstall, and it would take away the override certificate's role as the recovery path.
- **Verify the hand-off in the controller by dialling with the new credentials.** The controller has no new client key by design (ADR 0091), so it cannot.
- **Make `spec.paused` stop a started restore.** See 4.

## Consequences

- A `kubectl patch` of invalid `spec.cni.cilium.values` is refused at admission with the same messages as the UI. The `bpf.masquerade` / hostDNS refusal is still the UI's and the CLI's alone until the controller re-checks before the Job.
- A new `ValidatingWebhookConfiguration` entry `vichoruser-v1alpha1.kb.io` in `config/webhook/manifests.yaml` and `dist/chart/templates/webhook/validating-webhook-configuration.yaml`. With `failurePolicy: Fail`, a down manager refuses `IchorUser` writes within 5 s, as it does for the other kinds; the override certificate can remove the configuration.
- An Ichor user who holds `ichorusers` update rights through a custom RoleBinding can no longer grant more than they hold with `kubectl` either.
- Uninstall: deleting the namespace works (system identities are exempt); `kubectl delete ichoruser` of the last admin by a non-system identity is refused, and the override certificate does it.

## Amendments to earlier ADRs

ADR 0094 and ADR 0108 each get a pointer to this ADR; ADR 0099 gets the note that decision 4's controller re-check is not built.

## Amendment 2026-11-05: the controller re-checks the Cilium values before every Helm Job

Decision 1 left the three guards that need the machine configuration or the live cluster (`bpf.masquerade` against `forwardKubeDNSToHost`, KubePrism in kube-proxy-replaced mode, Gateway API CRDs) to the UI and the CLI, so a `kubectl patch` by an `operator` could still make the controller install values that break CoreDNS. The gap is closed in the controller.

- **One builder.** The context the UI backend built for `ValidateValues` moved to `internal/cnifacts` (`Source.Context`); the UI's `validationContext` calls it, and so does the controller (`Source.Validate`: the stored user values, the kube-proxy mode the Job runs with, the facts). The only differences are the inputs: the UI renders with a placeholder bundle and masked Secrets, the controller with the real bundle (`loadBundle`) and its own Secret reads. A fact that cannot be read stays unknown and its guard passes, as in the UI.
- **Where.** `AdvanceCNIHelmJob`, the one function every Cilium Helm Job goes through (hand-over and values change in `CNIHandoverReconciler`, kube-proxy switch and chart upgrade in the operation executor), takes a check and runs it only when the Job does not exist yet. A Job already running or finished is not re-checked, so a late edit of a patch cannot hide a completed Job. A refusal returns `*CNIInvalidValuesError` and creates nothing.
- **Hand-over and values change.** The reconciler releases the cluster operation lock it had taken, sets `CNIValuesApplied=False` with reason `InvalidValues` (the reason a parse failure of the values already uses; before the hand-over `CNIHandover` carries it too, after it `CNIHandover` stays True) with every problem in the message, and requeues after the normal 30 s. A later valid edit of the values, or of the patches the guards read, is picked up on the next pass. `spec` is never written.
- **Operations.** A `cni` upgrade or kube-proxy switch whose Job would be refused pauses with the problems as its failure reason, like any other error of its executor; fix the values and retry.
- **RBAC.** The controller may `get` the CRD `gateways.gateway.networking.k8s.io` by name (a marker on `CNIHandoverReconciler`, `config/rbac/role.yaml`), read through the uncached reader.
- **Not changed.** The webhook stays static (decision 1). A cluster whose stored values were valid when stored but fail the check now is refused a new Job until they are fixed; the running Cilium is untouched.

## Amendment 2026-11-05: the grant rule follows the built-in role hierarchy

The grant rule in decision 2 (and ADR 0108) was a literal match on the requester's `ichor:<role>` groups, so an `operator` could not create a user with `[viewer]`, although the `operator` ClusterRole includes every rule of `viewer` and the issued identity carries only the groups of the roles in `spec.roles`. `ichorauth.UngrantedRole` now honours the order viewer < operator < admin: a requester may grant any built-in role at or below the highest built-in role they hold. A custom role still needs a literal match, and an admin may grant anything, as before. An operator still cannot grant `admin`. The IchorUser webhook and the UI backend call the same function, so both change together.

## Amendment 2026-11-05: reverting a refused or failed edit clears the condition

After an edit was refused (`InvalidValues`) or its Job failed (`JobFailed`), reverting the values to exactly the applied ones left `CNIValuesApplied` False and `status.cni.jobFailures` set for good, because the reconcile returned at "the applied hash equals the wanted hash" before it wrote anything. That path now sets `CNIValuesApplied` True with reason `Applied` (the success path's reason and message) and clears `jobFailures` and `jobFailuresHash`, and writes status only when one of them changes. No Job runs, the lock is not taken and `spec` is not touched.

## Amendment 2026-11-05: changing the Cilium values is an admin's right (ADR 0131)

Decision 1 made the webhook check what the values contain, not who writes them. A value that passes the check can still be harmful by design: the Cilium chart renders `extraContainers`, `extraHostPathMounts` and image overrides into pods the Helm Job (a near-cluster-admin identity) creates on every node. The TalosCluster admission handler now also refuses a create or an update that changes `spec.cni.cilium.values` unless the requester holds `update` on `talosclusters/cilium-values`, which only `ichor-admin` holds; the UI backend and the CLI ask the same question first. The five `*.secretsNamespace.name` chart values became Ichor-owned keys, so this ADR's owned-key check refuses them.
