# 0139. Adopt captures a node's v1alpha1 `machine.network.interfaces` into its config patch

Status: Accepted · Date: 2026-11-10 · Design section: Configuration

Decided: when a node's live config carries `machine.network.interfaces` and `spec.network` is unset, adopt writes that subtree, and nothing else of the `Config` document, into the machine's `adopt-<machine>` `TalosConfigPatch` as a `kind: Config` document. `spec.network` stays empty.

## Context

A Talos v1.13 node with a static address under the v1alpha1 `machine.network.interfaces` was blocked by `ichor adopt --plan` on that path. Reading the addressing into `spec.network` does not help: for contracts above v1.11 `pkg/render/machinenetwork.go` renders the multi-document form (`LinkAliasConfig` net0, `LinkConfig`), so the render would drop the node's v1alpha1 block and add documents it lacks (11 changed paths), converting the running node and aliasing its link. With `spec.network` unset `injectNetwork` renders nothing, so the v1alpha1 block is pure node-only drift.

## Decisions

1. The interfaces subtree is captured through the existing patch path (`groupChangedPaths`, `captureDocuments`, the zero-drift verification), as a `Config` document holding `version` and `machine.network.interfaces` only. The patch merges into the baseline and reproduces the node byte for byte, so applying it changes nothing on the node.
2. Never the whole v1alpha1 document: it carries the machine and cluster secrets. Only the subtree is copied.
3. The mirror drops secret fields (a WireGuard private key), so a block holding one does not reproduce the node, and the verification refuses the adopt instead of dropping the key.
4. A node adopted this way keeps the v1alpha1 form until the user converts it; setting `spec.network` later is a separate, explicit change.

## Rejected

- Filling `spec.network` from the node: converts the node to the multi-document form.
- Rendering the v1alpha1 form for v1alpha1 nodes: a new CRD field and a render branch keyed on the node, not on the contract.
