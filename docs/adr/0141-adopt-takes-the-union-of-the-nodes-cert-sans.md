# 0141. Adopt takes the union of the nodes' certSANs into spec.certSANs

Status: Accepted · Date: 2026-11-10 · Design section: Bootstrap

Decided: `ichor adopt` sets a new cluster's `spec.certSANs` to the union of every node's `machine.certSANs` and every control plane's `cluster.apiServer.certSANs` (first-seen order), instead of requiring all of them to agree. A node that lacks some of the union gains them: those paths are class B (Ichor-owned, need consent) and the plan names the SANs each node gains. A node that states a SAN `spec.certSANs` lacks would lose it, and stays class C (blocks).

## Context

On a real three-control-plane Talos v1.13 cluster each node lists its own name and address next to the shared SANs, so `agreedCertSANs` returned nothing and `machine.certSANs` and `cluster.apiServer.certSANs` blocked every machine.

## Decisions

1. **One spec field drives both.** machinery's `generate.WithAdditionalSubjectAltNames` feeds `MachineCertSANs` (machine and worker configs) and the kube-apiserver's `certSANs`/`certExtraSANs` from the same list, so `spec.certSANs` needs no sibling field.
2. **The union is safe to render everywhere.** Talos documents `machine.certSANs` as extra subject alternative names added to the machine's certificate (interface IPs are added on top); more names only widen what the certificate is valid for. Ichor only ever adds names, never removes one.
3. **Consent, not silence.** The nodes' certificates do change, so the paths are listed under "Ichor-owned settings its first render will change (need consent)" with the gained names.
4. **Never lose a SAN.** The class B test is per node: every SAN the node states (machine, and for a control plane the apiserver's) must be in `spec.certSANs`. This matters for an existing cluster whose user-set `spec.certSANs` is narrower than a node's list.

## Rejected

- Per-node SAN patches: `machine.certSANs` is only capturable in the legacy document, which adopt never captures.
- A `--cert-sans` flag: the union is derivable; the user can still edit `spec.certSANs` of an existing cluster.
