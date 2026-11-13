# 0159. With kube-proxy enabled Ichor sets no KubePrism address, because the Talos guide's per-mode tabs say so

Status: Accepted · Date: 2026-11-14 · Design section: CNI · Touches: 0013, 0099, 0113

Decided: `k8sServiceHost` and `k8sServicePort` are set (`localhost`, `7445`) only when kube-proxy is replaced; with kube-proxy enabled neither is set and the chart's default applies, as the Talos v1.14 Cilium guide's kube-proxy-enabled tabs do.

## Context

Two Talos sources read differently on the kube-proxy-enabled case.

- The Talos v1.14 Cilium guide (`docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`, the page with its "For Talos v1.14 and later" branches) states as a prerequisite that it "assumes KubePrism is enabled and configured on port `7445`". Its kube-proxy-enabled tabs set neither `k8sServiceHost` nor `k8sServicePort`. Its kube-proxy-free tabs set `k8sServiceHost=localhost` and `k8sServicePort=7445`, for the Helm and Cilium CLI methods alike.
- The Talos v1.14 KubePrism page (`docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/system-configuration/kubeprism`) says, with no mode qualifier: "When using CNI components that require access to the Kubernetes API server, the KubePrism endpoint should be passed to the CNI configuration (e.g. Cilium, Calico CNIs)."

ADR 0013 names the KubePrism address for the kube-proxy-free mode only, and ADR 0113 describes the guide's values the same way. Neither decided the enabled case. Before this record, the two pages that describe Ichor's behaviour said KubePrism was used in every mode, which the code does not do.

## Decision

Ichor follows the guide's per-mode tabs. The KubePrism page's general sentence is read as describing the kube-proxy-free mode, and it is not applied to the kube-proxy-enabled mode. The `enabled` branch of the Cilium values sets neither key, and `OwnedKeys` still claims both keys in both modes, so a user value for either is refused in both.

KubePrism itself must still be enabled on port 7445 on every node in both modes. The no-CNI adopt checks require it in both modes, and ADR 0099 says the Cilium Job reaches the API through it.

The two reference pages that described KubePrism as used in every mode now describe the per-mode behaviour: `docs/reference/cli/ichor_bootstrap_cni.md` (generated from the `ichor bootstrap cni` help text) and the values diagram's alt text in `docs/concepts/cni.md`.

## Consequences

- With kube-proxy enabled, Cilium's API address is the chart's default, as the guide leaves it. Setting these keys in `spec.cni.cilium.values` is refused in both modes, so a change here needs the guide to change first.
- If the Talos guide later adds KubePrism to its kube-proxy-enabled tabs, or the KubePrism page's general sentence is qualified otherwise, this record is revisited and the code follows the guide.
- The values diagram in `docs/images/cni-values.light.drawio.svg` still labels the KubePrism address as applied to every owned key. It is not yet corrected.
