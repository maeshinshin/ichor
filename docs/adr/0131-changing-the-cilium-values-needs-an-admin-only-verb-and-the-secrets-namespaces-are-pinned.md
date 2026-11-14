# 0131. Changing the Cilium values needs an admin-only verb, and the Cilium Secrets namespaces are pinned

Status: Accepted · Date: 2026-11-05 · Design section: Trust, Webhooks, Cilium · Builds on: 0094, 0096, 0099, 0108, 0129

Decided: A user with only the built-in `ichor-operator` role could write `spec.cni.cilium.values` (in the UI, with `ichor apply cni-values`, or with `kubectl`) and so choose what the Cilium Helm Job deploys. The Job's ServiceAccount (`ichor-cni`) holds `escalate` and `bind` on ClusterRoles and Roles by name (ADR 0099 decision 2, ADR 0108), so it is as strong as cluster-admin, and the Cilium chart accepts `extraContainers`, `extraInitContainers`, `extraHostPathMounts`, `extraVolumes`, `extraArgs`, image overrides and `securityContext`: a privileged, host-network pod with a host path on every node is a legal value. That is root on every node, and from there the Talos and Kubernetes PKI and the Secrets in `ichor-system`: operator became admin-equivalent. This ADR (1) makes changing the values a right of its own, the verb `update` on `talosclusters/cilium-values`, held by `ichor-admin` only and checked by the TalosCluster admission webhook as the requesting user, so the UI, the CLI and `kubectl` obey it alike, and (2) makes the five `*.secretsNamespace.name` values of the chart Ichor-owned, pinned to `cilium-secrets`.

## Context

Review of the roles found two ways for a lower role to reach the Job's rights.

1. **The values are code.** ADR 0099 decision 4 validates the values (schema, unknown keys, owned keys, size) but cannot judge them: the chart is a general Helm chart, and any key it renders into a pod spec is available. Banning the dangerous keys one by one would be a list that a chart upgrade or a new alias silently breaks (`extraContainers`, `extraInitContainers`, `extraHostPathMounts`, `extraVolumes`, `extraArgs`, `image.override`, `securityContext.privileged`, `podSecurityContext`, `envoy.extra*`, ...). The right to write the layer has to be an administrative right, like `spec.authentication` (ADR 0094 decision 4) and the Secret references (ADR 0096), which the same webhook already gates with a SubjectAccessReview on a subresource.
2. **The namespace of the Secrets Roles.** The chart renders Roles named `cilium-ingress-secrets`, `cilium-gateway-secrets`, `cilium-envoy-config-secrets`, `cilium-bgp-control-plane-secrets` and `cilium-tlsinterception-secrets` (and the operator's counterparts, with RoleBindings) in the namespace given by `ingressController.secretsNamespace.name`, `gatewayAPI.secretsNamespace.name`, `envoyConfig.secretsNamespace.name`, `bgpControlPlane.secretsNamespace.name` and `tls.secretsNamespace.name`. The Job may create those Roles by name in any namespace (the `roles` rule is cluster-wide, `escalate` and `bind` are by name). None of the five keys was Ichor-owned, so a value of `ichor-system` rendered a Role that lets the Cilium ServiceAccounts read the Secrets there. The chart's own defaults are `cilium-secrets`, except BGP's, which is `kube-system`.

In the embedded 1.20.4 chart with the features on (`ingressController`, `gatewayAPI`, `envoyConfig`, `bgpControlPlane`, `tls`), these five values, and no other, decide the namespace of a Role or RoleBinding that holds Secret access. The other values that name a namespace (`authentication.mutual.spire.install.namespace`, `clustermesh.mcsapi.corednsAutoConfigure.coredns.namespace`, `*.serviceMonitor.namespace`, `dashboards.namespace`, `k8sServiceLookupNamespace`) render Roles and objects the Job has no `escalate` or `bind` for, by name, or are covered by the first point.

## Decision

### 1. A subresource verb for the values

The verb is `update` on `talosclusters/cilium-values`, the same shape as `talosclusters/authentication` and `talosclusters/secret-references`: the constants are `ichorv1alpha1.CiliumValuesVerb` and `ichorv1alpha1.CiliumValuesSubresource`.

- **Where it is enforced.** `OverrideHandler.handleCluster` (the admission handler that already gates `spec.authentication` and the Secret references, `internal/webhook/v1alpha1/override_webhook.go`) refuses a create or an update when `spec.cni.cilium.values` differs from the stored object's, unless the requester holds the verb on that cluster (a SubjectAccessReview for `req.UserInfo`, so a hand-written CR obeys it too). On create, "differs" means non-empty. The comparison is on the decoded JSON, so spacing and key order are no change, and an unset, `null` and `{}` layer are the same. An update that leaves the values as they are is never asked, so an operator can keep editing everything else on a cluster whose values an admin set.
- **Who is exempt.** The exemptions of that handler stay as they are (the controller, the namespace and garbage collectors). The controller never writes a `TalosCluster` spec (ADR 0116, enforced by `limitController`), so it never changes the values; the automatic patcher writes only the two version fields. Neither is affected.
- **Who holds it.** `ichor-admin` only (`config/rbac/ichor_admin_role.yaml` and `dist/chart/templates/rbac/ichor-user-roles.yaml`; `dist/chart/user_roles_test.go` compares them and checks that viewer and operator lack it). `ichor-operator` keeps `update` and `patch` on `talosclusters` and can still start a Cilium upgrade (the chart is the one Ichor embeds, and the stored values are validated again before the Job, ADR 0129). To delegate the right, bind a ClusterRole with the verb.
- **The UI and the CLI.** The UI backend writes as the impersonated user, so the webhook already covers it. `SetCiliumValues` asks the same question first (`mayAll`, as for the other subresource verbs) and refuses with a `PermissionDenied` that names the verb, and `GetCiliumValuesResponse.can_edit` (a new field 7) tells the Cilium page whether the caller may: for an operator the page shows the layer read-only with an explanation instead of an editor. `ichor apply cni-values|edit` read `can_edit` before they validate, show the diff or open the editor, so the refusal comes before the confirmation prompt. A write that leaves the layer as it is asks for nothing.
- **Other writers of the values.** `ichor adopt --cilium-values` creates the `TalosCluster` with values, so it needs the right too (an adoption was already an admin's act). `ichor bootstrap cni` renders a manifest locally and writes no `TalosCluster`.

### 2. The secretsNamespace names are Ichor-owned

`pkg/cni/cilium` adds the five `*.secretsNamespace.name` keys to `OwnedKeys` and sets them to `cilium-secrets` in Ichor's values (`secretsNamespaceOf`). A user value for any of them, whatever it is (even `cilium-secrets`, an empty string or `null`), is refused as "set by Ichor and cannot be overridden" by `ValidateValues`, in the webhook, the backend, the CLI and the controller's check before every Job. The owned layer applies last, so the rendered Roles are always in `cilium-secrets`, which the chart creates (`tls.secretsNamespace.create` is true by default).

- **BGP.** The chart's default for `bgpControlPlane.secretsNamespace.name` is `kube-system`, so a cluster that enables BGP now has its Role in `cilium-secrets`, and BGP authentication Secrets belong there. This is the one behaviour change of this decision.
- **Takeover.** `ConfigMapValues` reads the five names (and the switches of ingress, Gateway API and BGP) back from the live `cilium-config`, so `OwnedKeyConflicts` reports a taken-over Cilium whose BGP secrets are in `kube-system`. A feature that is off is not a conflict (its chart default is only a default). The Envoy-config switch is not read back from an inline manifest (its ConfigMap key is also set by ingress and Gateway API), which `shortcut:` marks.
- **What is not pinned.** `tls.readSecretsOnlyFromSecretsNamespace=false` gives the Cilium agent a cluster-wide `get`/`list`/`watch` on Secrets, and the chart's operator ClusterRole holds the same when ingress, Gateway API, BGP or secret sync is on. Both are Cilium's own design for those features and sit behind decision 1, so only an admin can set them. They are not owned.

## Alternatives rejected

- **A deny list of dangerous chart keys.** It must be complete for every chart version Ichor embeds and every alias, and an omission is root on the nodes. The admin-only verb needs no list and covers the keys no one has thought of.
- **A validating admission policy on the rendered Job's pods.** It would judge what the Job creates rather than who asked, but Ichor does not own the admission of the managed cluster (and it would be the user's cluster configuration, not Ichor's), and the Job's pods are the chart's own.
- **Giving the Job a narrower ServiceAccount.** The `escalate`/`bind` pair is the reason the Job exists as a separate identity (ADR 0099 decision 2); the chart's ClusterRoles need it. Narrowing it is not possible for the chart's cluster-scoped objects.
- **Checking only in the UI backend.** It would leave `kubectl`, which is the rule ADR 0129 exists to enforce.
- **Pinning `create` as well as `name`.** The name decides where the Role lands; `create` only decides whether the namespace object is rendered, and a user who turns it off breaks the install, not Ichor's security.

## Consequences

- An operator can no longer change Cilium's values by any route; the Cilium page shows them read-only, `ichor get cni-values|diff cni-values` and the validation calls still work. A cluster whose values an admin set stays editable by an operator in every other field.
- The five names are visible, with the reason, in `ichor get cni-values --layer owned` and in the Cilium page's owned keys.
- A custom role that today holds `update` on `talosclusters` and edits the values loses that until it also holds the new verb.

## Risks

- A user who relied on BGP Secrets in `kube-system` must move them to `cilium-secrets`; a takeover reports the difference before it changes anything.
- The right is as strong as the Job. An admin who holds it is trusted with root on the nodes, as an admin already is through `override` and `start`; ADR 0108's statement that no built-in role is cluster-admin stands, and `ichor-admin` is not given `escalate`.
- A role binding that grants `*` on `talosclusters/*` grants the verb too; the chart's invariant tests keep the built-in roles free of wildcards.

## Amendment (2026-11-06): the agent's cluster-wide Secret read is pinned off

"What is not pinned" above left `tls.readSecretsOnlyFromSecretsNamespace=false` to the admin-only verb. That is not enough for the agent: the Cilium agent is a privileged pod on every node, and with the flag false the chart's agent ClusterRole grants it `get`, `list` and `watch` on Secrets of every namespace, so one compromised node exposes `ichor-system` (the secrets bundle with the CA keys). Ichor hardens itself strictly, and nothing Ichor installs needs that read, so the agent's read is now Ichor's, like the five namespaces.

- **Owned keys.** `tls.readSecretsOnlyFromSecretsNamespace` and its deprecated alias `tls.secretsBackend` (`k8s` means false, `local` means true; the chart's `readSecretsOnlyFromSecretsNamespace` helper lets the flag win, then the alias, then defaults to true) are in `OwnedKeys`. A user value for either is refused as "set by Ichor", an admin's included. The effective values carry `readSecretsOnlyFromSecretsNamespace: true` explicitly, so a chart that changes its default cannot widen the read. The alias is left unset, which the flag overrides.
- **Takeover.** The chart writes `policy-secrets-only-from-secrets-namespace: "true"` into `cilium-config` only while the flag is true (charts 1.19 and 1.20). `ConfigMapValues` reads it, and `overlayObserved` reads its absence from an existing ConfigMap as false, so a running Cilium with the cluster-wide read is an owned-key conflict (informational, like the namespaces), reported before the adoption narrows it. A release whose values set the flag to false or the alias to `k8s` is reported the same way; the chart's unset flag and the alias `local` equal Ichor's value and are not conflicts. The alias has no ConfigMap key and is read from the release values only.
- **Rendering.** `BootstrapManifest` renders the `cilium` agent ClusterRole with no rule on `secrets`, with or without ingress, Gateway API, Envoy config and BGP on.
- **Still not pinned.** The operator's cluster-wide Secret read with ingress, Gateway API, BGP or secret sync on is unchanged: it is the operator Deployment, not a privileged pod on every node, and stays behind the admin-only verb.
- **Consequence.** A TLS-aware `CiliumNetworkPolicy` (`terminatingTLS`, `originatingTLS`) must reference Secrets in `cilium-secrets`. A takeover of a Cilium that read Secrets cluster-wide reports it first.
