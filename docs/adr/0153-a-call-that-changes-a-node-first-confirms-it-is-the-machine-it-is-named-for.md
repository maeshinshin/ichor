# 0153. A call that changes a node first confirms it is the machine it is named for

Status: Accepted · Date: 2026-11-12 · Design section: Nodes · Amends: 0135, 0136

Decided: before Ichor wipes a node, makes it leave etcd, applies a configuration to it, upgrades it or runs a node action on it, it reads the node's machine UUID over the same connection and compares it with `spec.identity.uuid`. A UUID that differs, an empty one, and an Identity call that fails all refuse: nothing is sent. The name of the Kubernetes Node and etcd member a removal acts on comes from `spec.hostname`, then `status.nodeName`, and from the node's own report only when that node has passed the same check.

## Context

A machine's endpoint is an address. After a DHCP change it can answer as another control plane that trusts the same CA, and every credential Ichor holds opens both. The read path already compared UUIDs (`checkEndpointIdentity`), but it only reads, so it lets an unreadable or empty UUID pass. The calls that change something did not check at all: `ichor delete node A` could wipe B, or ask B to leave etcd while Ichor believed A's member was gone. A removal also took the Node name to drain and the etcd member to remove from whatever answered at A's address, so B could be drained and removed from etcd through a survivor even when B itself was never asked to do anything.

## Decisions

1. **One check, `confirmMachine`, run right after the dial.** `resetNode` and `trySelfLeave` call it before their RPC; the configuration rollout, the Talos upgrade and the node actions call it before they read or write the node, so a node of the wrong machine is not mistaken for "already upgraded" either.
2. **Refusing is the default for everything that is not a match.** An Identity error cannot show the node is the right one, and neither can a node with no UUID; the spec's UUID is required, so a machine that has one never legitimately matches an empty answer. Refusing costs a retry: the removal and the operations already treat an unreachable node as a thing to wait for, and the self-leave falls back to removing the member through a survivor, which does not touch the node at the address.
3. **While a node is expected to be away, a refusal is a wait.** After the Upgrade or Reboot call has been accepted, or while the API server restarts, an unconfirmed node is handled like an unreachable one (the existing settling and restart windows). A refusal before the call fails or pauses the step with the text that says nothing was sent.
4. **A removal prefers a name Ichor recorded.** `status.nodeName` is written from the machine's own answer on a read that did not find another machine's UUID, and once, so a later change of the address does not change it. A node that answers at the address and fails the check names nothing, and the Kubernetes Node found by address is not used as a fallback then; a control plane whose name cannot be told is refused, as before.

## Rejected

- Treating an empty UUID or an Identity error as a pass, as the read path does: it leaves the hole open in exactly the cases where the node is half-working.
- Comparing serial numbers or MAC addresses as well: the UUID is the primary key of the identity, and the others are hints that a replaced board changes.

## Risks

The other calls that dial a machine and change it (shutdown cluster, the etcd restore, the Kubernetes upgrade, the CA and key rotations, the CNI switch) do not run the check yet. The Kubernetes Node found by address is still the last resort for the name of a control plane that cannot be dialed and has no recorded name.
