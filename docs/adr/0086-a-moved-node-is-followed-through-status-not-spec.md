# 0086. A moved node is followed through status, not spec

Status: Accepted · Date: 2026-10-27 · Design section: Nodes (discovery), Configuration reconcile
Decided: the controller never mutates `TalosMachine.spec.endpoint`; when a node answers somewhere else, the address is recorded in `status.observedEndpoint` (with `status.observedEndpointFor`, the `spec.endpoint` it replaces) by discovery, which matches the identity (ADR 0011), or by the machine reconciler, which reads the InternalIP of the node's Kubernetes Node; every dial of the node's Talos API uses `DialEndpoint()` (the observed address while it still supersedes `spec.endpoint`, else `spec.endpoint`); a converged machine is re-probed every two minutes so Ready turns False when the node is gone

## Context

On hardware, a power-cycled KubeVirt node came back with a new pod IP. `spec.endpoint` kept the old one, so a shutdown operation never saw the node return, the machine stayed Ready=True because nothing re-probed a converged machine, and a node that was reset and showed up in maintenance mode at its new address was not matched to its `TalosMachine`. The project rule is that the controller never mutates `spec`; the architecture says discovery matches identity by UUID, serial and MACs (ADR 0011) but not what happens when the same identity appears at another address, and `Discover` returned the existing object untouched.

## Decision

**Where the address lives.** `status.observedEndpoint` holds the address and `status.observedEndpointFor` the `spec.endpoint` it was recorded against. `TalosMachine.DialEndpoint()` returns the observed address only while `observedEndpointFor == spec.endpoint`, so an operator who edits `spec.endpoint` overrides what was observed without having to clear a status field. The admin client (`dialAdmin`), the maintenance probe and the fallback in `resolveNodeName` use `DialEndpoint()`.

**Who writes it.** (1) `Discover`: the same identity answering in maintenance mode at an address other than `DialEndpoint()` is recorded and the object is otherwise returned unchanged; this is the reset-and-rediscovered case. (2) The machine reconciler: when a machine that has applied a configuration cannot be read and has a `spec.hostname`, the Kubernetes Node of that name says where the node is now (the kubelet keeps its InternalIP current); if that differs from `DialEndpoint()` it is recorded and the next round dials it. Both write through a re-read and retry on conflict.

**Ready.** A converged machine requeues after `reachabilityRecheck` (two minutes), so an unreachable node ends up Ready=False with `CurrentConfigUnavailable` instead of staying True until something else touches the object.

## Consequences

- The CRD gains two status fields. `kubectl get` still shows `spec.endpoint`; the UI and the CLI dial `spec.endpoint` in their own clients (`internal/uiapi`, `internal/cli`) and should move to `DialEndpoint()` as a follow-up.
- A machine with no `spec.hostname` is not followed through its Kubernetes Node (asking the node is what just failed); it is still found again by discovery when it is in maintenance mode. Recording the node name in status would close that gap.
- When every control plane moves at once the Kubernetes API is down and nothing can be read from it; the operator edits `spec.endpoint`.
- Not verified on hardware: the KubeVirt power-cycle itself. Tests use fakes and envtest.
