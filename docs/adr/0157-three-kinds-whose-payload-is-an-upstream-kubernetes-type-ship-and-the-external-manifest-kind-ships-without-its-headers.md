# 0157. Three kinds whose payload is an upstream Kubernetes type ship, and the external manifest kind ships without its headers

Status: Accepted · Date: 2026-11-13 · Design section: Typed configuration · Builds on: 0150, 0152, 0155

Decided: `KubeAuditPolicyConfig`, `KubeAuthorizerConfig` and `KubeCredentialProviderConfig` are added to the union (fields `kubeAuditPolicyConfig`, `kubeAuthorizerConfig`, `kubeCredentialProviderConfig`), each carrying its free-form payload as the upstream Kubernetes type the document's own reference names, through the same field-override table the `v1alpha1` document already uses for those three types. `KubeExternalManifestConfig` is added (field `kubeExternalManifestConfig`) with its `headers` map dropped as a secret field, so the kind fetches a manifest only from a URL that needs no authentication.

## Context

ADR 0155 held back nine kinds whose payload is a `meta.Unstructured` field and one kind with a credential-shaped map. Three of the nine carry a payload the `v1alpha1` document's mirror already carries as an upstream type: the audit policy (`k8s.io/apiserver` audit `Policy`), the webhook authorizer configuration (`k8s.io/apiserver` `WebhookConfiguration`) and the kubelet credential provider configuration (`k8s.io/kubelet` `CredentialProviderConfig`). Those types generate a CRD schema, need no new module, and are small. The fourth kind has a `headers` map whose usual content is an `Authorization` header, which is the container ADR 0152 retired from the `v1alpha1` document as `extraManifestHeaders`.

## Decisions

1. **The payload is the upstream type, not a trimmed copy.** Nothing the Kubernetes type can express is lost, and the document's own text says its value is the literal Kubernetes configuration. The types are converted to and from the document's free-form field through their JSON form, as the `v1alpha1` document's three fields of the same types are.
2. **`headers` is dropped, whole.** The same reasoning as ADR 0152: a map of header names to values cannot be split into harmless and secret entries, and keeping the field keeps a place to store a bearer token in a custom resource. The kind ships because a public manifest URL needs no header. A private source stays outside Ichor until a reference form exists, the follow-up ADR 0012 and ADR 0152 already name. A Talos document that carries `headers` loses them when it is converted to the typed form, as any other secret field does.
3. **Reviewed names.** The authorizer's `type` and `webhook` fields and the credential provider's `configuration` field match the credential-shaped name scan. None holds a credential: the first is an enum, the webhook configuration names a kubeconfig file on the node, and a credential provider configuration names the plugin that fetches credentials at pull time. Each is a row in the reviewed list with that reason.
4. **Field order.** The four fields are appended to the union after the existing ones; ADR 0150's append-only rule is unchanged.

## Rejected

- Keeping `headers` and refusing a value in the webhook: it guards only the paths that remember to call it (ADR 0012, ADR 0152).
- Mirroring the three upstream types field by field: the mirror would have to track Kubernetes releases and would lose whatever it did not copy.

## Consequences

Each of the four kinds is paid twice in CRD size, because `TalosCluster` embeds the union. A patch that needs an authenticated manifest source cannot express it through this kind.
