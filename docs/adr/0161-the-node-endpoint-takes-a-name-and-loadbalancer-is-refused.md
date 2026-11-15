# 0161. The node endpoint takes a name, and loadBalancer is refused

Status: Proposed · Date: 2026-11-15 · Design section: API · Touches: 0099, 0101, 0128

Decided: The `node` endpoint type takes an IP address or a DNS host name, so a load balancer's name or address goes in `node`; `loadBalancer` stays in the enum, deprecated, and the webhook refuses an endpoint of that type when it is written, with a message that says to use `node`.

## Context

`vip`, `loadBalancer` and `node` differed in two places only. `CheckAddress` rejected a name for `vip` and `node`, and checked nothing for `loadBalancer`, so any string passed (a defect, not a feature). `ichor adopt` defaulted to `loadBalancer` for an address that was not a node's own.

Nothing else reads the type. The kubeconfig and the rendered API server address come from the address (`https://ADDRESS:6443`); the controller's egress allowance admits a name as a domain name; only `vip` adds a Layer 2 VIP. So the type said what the address already says, and the hostname was the only thing that kept the two apart.

The type was not free, though. The Kubernetes upgrade warned that a `node` endpoint is one control plane's address and that a load balancer should replace it. With `node` taking a name, that warning would tell a cluster behind a load balancer to get one.

## Decision

- `node` takes an IP address or a DNS host name. A name is a lower-case RFC 1123 name, each label at most 63 characters and the whole at most 253, with no trailing dot; a last label that is all digits is refused, so a mistyped IP address such as `192.168.1.300` is not taken for a name. `vip` still takes an IP address only.
- `loadBalancer` stays in the CRD enum, so stored objects still read and no cluster is broken by the change. It is deprecated: the webhook refuses a create with it, and an update that changes the endpoint and leaves it at `loadBalancer`. The message names `node`. An update that leaves the endpoint as it is, such as a finalizer, a label or a deletion, is not refused, so a stored `loadBalancer` never blocks its own removal. Moving the type to `node` in an update is allowed. The controller does not branch on the type, so a stored `loadBalancer` cluster keeps rendering and running.
- `ichor init` offers `vip` and `node`, and refuses `loadBalancer` by name.
- `ichor adopt` uses `node` for every endpoint that is not a VIP. adopt never claims an address it did not configure, and `node` is the one type that describes an address Ichor does not configure.
- The Kubernetes upgrade warning fires when the endpoint is the address of an approved control plane, not when the type is `node`. A name in front of several control planes does not warn. A name that resolves to one control plane alone does not warn either: Ichor cannot tell from the name, and the warning is about a specific control plane.

## Consequences

- A stored `loadBalancer` cluster migrates by one edit, `spec.endpoint.type: node`. The address and the kubeconfig do not change.
- The `vip` and `node` types keep their meaning. `node` no longer implies "the first control plane's own address".
- The alternatives were refused. Removing `loadBalancer` from the enum fails every stored object that carries it, as ADR 0101 and ADR 0099 explain for the removed CNI presets. Accepting any string for `node` would keep the defect the check was added for.
