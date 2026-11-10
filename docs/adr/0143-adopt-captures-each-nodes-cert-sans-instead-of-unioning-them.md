# 0143. Adopt captures each node's certSANs instead of unioning them

Status: Accepted · Date: 2026-11-11 · Design section: Bootstrap · Supersedes: 0141

Decided: `ichor adopt` no longer takes the union of the nodes' certSANs. `spec.certSANs` is the list the control planes share in their kube-apiserver SANs (else the `machine.certSANs` every node states, else empty), and whatever a node's own `machine.certSANs` (and legacy `cluster.apiServer.certSANs`) hold beyond it is captured into that node's `adopt-<machine>` patch, as the network interfaces (ADR 0139) and ResolverConfig (ADR 0140) are. Nothing changes on a node, and the "need consent" lines and the per-node "gains the subject alternative names" line are gone.

## Context

A real VM run of ADR 0141 showed its cost: `machine.certSANs` is reboot-required, so every control plane went Ready=False with `RebootRequired`, adopt's release step timed out after 10 minutes ("did not become Ready within 10m0s after its hold was released; the machines after it stay held"), and the operator had to run `ichor config apply` once per machine and adopt again. Adopting a three-control-plane cluster must not reboot it.

## Decisions

1. **Agree: spec; differ: capture.** When every node agrees, the list stays in `spec.certSANs` and no patch is written. When they differ, the shared kube-apiserver list still goes into `spec.certSANs` (it is identical across control planes, and `KubeAPIServerConfig` is not a patch kind, so this is the only zero-drift route for it), and each node's remainder is captured.
2. **The patch holds the remainder only.** A patch's list is appended to the rendered one, not merged, so the captured subtree is the node's SANs that `spec.certSANs` does not render; the render is then `spec ++ remainder`.
3. **Subtree only.** Like the interfaces capture, only `machine.certSANs` and `cluster.apiServer.certSANs` of the `Config` document are copied, never the document.
4. **Still class C where a patch cannot reproduce the node.** A node that lacks a SAN `spec.certSANs` adds (a patch cannot remove one), or lists them in another order, is blocked by the zero-drift check, with `certSANsHint`. Kube-apiserver SANs that differ between control planes are in `KubeAPIServerConfig`, which cannot be captured, and also block.

## Consequences

The union, its consent lines (`SANsGained`, `SANsCovered`, `sansGained`) and the class B treatment of the SAN paths are removed; nothing needed them.
