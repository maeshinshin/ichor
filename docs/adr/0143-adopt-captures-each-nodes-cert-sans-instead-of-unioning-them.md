# 0143. Adopt captures each node's certSANs instead of unioning them

Status: Accepted · Date: 2026-11-11 · Design section: Bootstrap · Supersedes: 0141

Decided: `ichor adopt` no longer takes the union of the nodes' certSANs. `spec.certSANs` is only the SANs every node has in both its `machine.certSANs` and, on a control plane, its kube-apiserver SANs, and whatever a node's own `machine.certSANs` (and legacy `cluster.apiServer.certSANs`) hold beyond it is captured into that node's `adopt-<machine>` patch, as the network interfaces (ADR 0139) and ResolverConfig (ADR 0140) are. Nothing changes on a node, and the "need consent" lines and the per-node "gains the subject alternative names" line are gone.

## Context

A real VM run of ADR 0141 showed its cost: `machine.certSANs` is reboot-required, so every control plane went Ready=False with `RebootRequired`, adopt's release step timed out after 10 minutes ("did not become Ready within 10m0s after its hold was released; the machines after it stay held"), and the operator had to run `ichor config apply` once per machine and adopt again. Adopting a three-control-plane cluster must not reboot it.

The first version took the control planes' shared kube-apiserver list, else the shared `machine.certSANs`. On a real cluster that set `localhost` in `spec.certSANs`, which the render then added to `cluster.apiServer.certSANs` where the node had none, and the zero-drift check then reported the interfaces captured in the same document as unrepresentable too.

## Decisions

1. **Share in both places: spec; the rest: capture.** The render feeds `spec.certSANs` into `machine.certSANs` and the kube-apiserver SANs of every node, and a patch can add a SAN but never take one away. So the list is the intersection of every node's `machine.certSANs` and every control plane's kube-apiserver SANs, in the first control plane's order. A real v1.13 cluster has `machine.certSANs: [localhost]` and no `cluster.apiServer.certSANs`: the intersection is empty, nothing is rendered anywhere, and `localhost` is captured per node.
2. **The patch holds the remainder only.** A patch's list is appended to the rendered one, not merged, so the captured subtree is the node's SANs that `spec.certSANs` does not render; the render is then `spec ++ remainder`.
3. **Subtree only.** Like the interfaces capture, only `machine.certSANs` and `cluster.apiServer.certSANs` of the `Config` document are copied, never the document.
4. **Still class C where a patch cannot reproduce the node.** A node that lacks a SAN `spec.certSANs` adds (a patch cannot remove one), or lists them in another order, is blocked by the zero-drift check, with `certSANsHint`. Kube-apiserver SANs a control plane has beyond the shared ones are captured as `cluster.apiServer.certSANs` on a v1.13 or older contract, but on v1.14 they are in `KubeAPIServerConfig`, which cannot be captured, and block. When the zero-drift check fails, only the paths that still differ are reported, so a path that was captured (the interfaces) is not blocked along with the one that was not.

## Consequences

The union, its consent lines (`SANsGained`, `SANsCovered`, `sansGained`) and the class B treatment of the SAN paths are removed; nothing needed them.
