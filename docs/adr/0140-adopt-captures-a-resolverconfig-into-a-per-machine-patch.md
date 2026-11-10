# 0140. Adopt captures a node's ResolverConfig into a per-machine patch

Status: Accepted · Date: 2026-11-10 · Design section: Adopt · Amends: 0138

Decided: `ichor adopt` converts a live `ResolverConfig` document into a `TalosConfigPatch` document, as it does `SysctlConfig` and `KernelModuleConfig`, in the machine's own `adopt-<machine>` patch (layer `machine`). A machine whose `spec.network.dnsServers` is set is not captured: a ResolverConfig difference stays unrepresentable and blocks, with the existing hint to set the servers in one place.

## Context

ADR 0138 let a patch carry a ResolverConfig, but adopt still listed `ResolverConfig.apiVersion`, `.kind` and `.nameservers` as unrepresentable, so a cluster with DNS nameservers stayed blocked.

## Decisions

1. **Capture, through the generated conversion.** The kind joins `groupChangedPaths` and `captureDocuments`; the zero-drift render check that already guards the patch applies unchanged.
2. **Per machine, not shared.** Adopt builds one patch per machine and the plan, apply and zero-drift check are per machine. A shared cluster or role patch would need a second pass that compares nodes and re-verifies every machine against it, which is more than a small change. Nodes with identical nameservers get identical machine patches that the user can merge into one cluster-layer patch by hand.
3. **`spec.network.dnsServers` wins.** The render refuses a patch ResolverConfig for such a machine (ADR 0138), so capturing would produce a patch that fails. The difference is reported as class C instead of being dropped silently.

## Risks

N identical per-machine patches on a large cluster until the user merges them.
