# 0159. With kube-proxy enabled Ichor sets no KubePrism address, because the Talos guide's per-mode tabs say so

Status: Accepted · Date: 2026-11-14 · Design section: CNI · Touches: 0013, 0099, 0113

Decided: `k8sServiceHost` and `k8sServicePort` are set (`localhost`, `7445`) only when kube-proxy is replaced; with kube-proxy enabled neither is set and the chart's default applies, as the Talos v1.14 Cilium guide's kube-proxy-enabled tabs do. Because Ichor sets nothing there, the operator may set them in `spec.cni.cilium.values`; they are Ichor-owned, and refused, only when kube-proxy is replaced.

## Context

Two Talos sources read differently on the kube-proxy-enabled case.

- The Talos v1.14 Cilium guide (`docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`, the page with its "For Talos v1.14 and later" branches) states as a prerequisite that it "assumes KubePrism is enabled and configured on port `7445`". Its kube-proxy-enabled tabs set neither `k8sServiceHost` nor `k8sServicePort`. Its kube-proxy-free tabs set `k8sServiceHost=localhost` and `k8sServicePort=7445`, for the Helm and Cilium CLI methods alike.
- The Talos v1.14 KubePrism page (`docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/system-configuration/kubeprism`) says, with no mode qualifier: "When using CNI components that require access to the Kubernetes API server, the KubePrism endpoint should be passed to the CNI configuration (e.g. Cilium, Calico CNIs)."

ADR 0013 names the KubePrism address for the kube-proxy-free mode only, and ADR 0113 describes the guide's values the same way. Neither decided the enabled case. Before this record, the two pages that describe Ichor's behaviour said KubePrism was used in every mode, which the code does not do.

## Decision

Ichor follows the guide's per-mode tabs. The KubePrism page's general sentence is read as describing the kube-proxy-free mode, and it is not applied to the kube-proxy-enabled mode. The `enabled` branch of the Cilium values sets neither key.

Ownership of the two keys follows the mode. A key is Ichor-owned when Ichor sets it, because a user value would fight Ichor's. With kube-proxy replaced Ichor sets both, so a user value is refused. With kube-proxy enabled Ichor sets neither, so there is nothing to fight, and the operator's value reaches the effective values like any other user value. `cilium.OwnedKeysFor(mode)` is the owned set for a mode: `OwnedKeys` without the two keys when the mode is `enabled`. Validation, the effective values and the UI's owned-key list read it. This was chosen over changing the decision so that `enabled` also points Cilium at KubePrism, which contradicts the guide's kube-proxy-enabled tabs, and over leaving the chart default unchangeable, which gives the operator no way to set an address Ichor does not manage. The refusal in replaced mode says that Ichor sets the key because `spec.cni.kubeProxy` is replaced, and that with kube-proxy enabled the operator may set it. Before this change it said Ichor sets the key, which was untrue of `enabled`.

KubePrism itself must still be enabled on port 7445 on every node in both modes. The no-CNI adopt checks require it in both modes, and ADR 0099 says the Cilium Job reaches the API through it.

The two reference pages that described KubePrism as used in every mode now describe the per-mode behaviour: `docs/reference/cli/ichor_bootstrap_cni.md` (generated from the `ichor bootstrap cni` help text) and the values diagram's alt text in `docs/concepts/cni.md`.

## Consequences

- With kube-proxy enabled, Cilium's API address is the chart's default, as the guide leaves it, until the operator sets `k8sServiceHost` and `k8sServicePort` in `spec.cni.cilium.values`. Ichor does not check that the address is reachable from every node.
- A value set in `enabled` mode is refused when the cluster later switches to `replaced`, because Ichor then sets both keys. The webhook checks the values again on a kube-proxy change, and the refusal names the key and says why.
- Setting these keys in `replaced` mode stays refused.
- If the Talos guide later adds KubePrism to its kube-proxy-enabled tabs, or the KubePrism page's general sentence is qualified otherwise, this record is revisited and the code follows the guide.
- The values diagram in `docs/images/cni-values.light.drawio.svg` labels the KubePrism address as set only when kube-proxy is replaced, as this record decides.
