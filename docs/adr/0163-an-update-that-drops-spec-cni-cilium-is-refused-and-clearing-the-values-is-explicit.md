# 0163. An update that drops spec.cni.cilium is refused, and clearing the values is explicit

Status: Accepted · Date: 2026-11-15 · Design section: CNI · Touches: 0013, 0131

Decided: the webhook refuses an update that leaves a cluster with no `spec.cni.cilium` while the stored object held Cilium values, which is what a whole-object write with `cni: {}` or with no `cilium` block does once the defaults fill in `preset` and `kubeProxy`; clearing the values on purpose is `spec.cni.cilium: {}`, which is what `ichor apply cni-values` with an empty file writes.

## Context

`spec.cni` defaults `preset` to `cilium` and `kubeProxy` to `enabled`, so `cni: {}` is a valid object. A write that carries it replaces the stanza: `kubectl apply` of a whole manifest, `kubectl replace` and a JSON-patch `replace` of `/spec/cni` all left `cni.cilium.values` gone with no event and no condition. A merge patch of `{}` changes nothing, and server-side apply of `cni: {}` is refused by the CRD schema for an unrelated reason.

The `ichor-cni-kubeproxy-switch` admission policy does not cover this. It compares `kubeProxy` only, so it refused the write on a cluster whose `kubeProxy` was `replaced` (the default turns it back to `enabled`) and said nothing on one whose `kubeProxy` was already `enabled`. The existing check that `spec.cni` cannot be removed compares the new stanza with the zero value, which the defaults have already filled in, so it cannot see `cni: {}` either.

A validating webhook sees the object after defaulting, so `cni: {}` and `cni: {preset: cilium, kubeProxy: enabled}` reach it identically. The two cannot be told apart, and the second is an ordinary way to write a manifest that forgot the block.

## Decision

- The webhook refuses an update when the stored object has Cilium values and the new one has no `spec.cni.cilium`. The message says that the update would drop them and how to clear them on purpose.
- `spec.cni.cilium: {}` is the explicit clear, and it is what the UI and `ichor apply cni-values` already write when the layer is emptied, so neither changes.
- Refusing was chosen over keeping the values. Keeping would need a mutating webhook on update, which the defaulter deliberately is not (an existing spec is never touched), and it would make a stanza that reads as empty hold values. Refusing also leaves an operator who does mean to reset the values a one-line way to say so.
- No schema change: the rule is in the webhook beside the existing `spec.cni` removal check.

## Consequences

- A GitOps repository that removes the `cilium` block to drop the values must write `cilium: {}` instead; the refusal names it.
- An update that keeps the values, or does not touch `spec.cni`, is unaffected, and a cluster with no values is unaffected, so the existing admission of `cni: {}` for such a cluster stands.
- Changing the values still needs the admin verb (ADR 0131); this rule is about the omission, not who may change them.
