# 0108. Security hardening

Status: Accepted; in progress · Date: 2026-11-02 · Design section: Trust, Deployment, Nodes · Touches: 0011, 0041, 0058, 0063, 0089, 0103, 0105, 0107

Decided: Ichor is hardened area by area after five read-only audits (2026-11-02): ichor-ui and its auth, the controller's privileges and admission, workloads and the supply chain, the security of the clusters Ichor creates, and secrets and credentials. Fixes to Ichor itself are made directly. For the clusters Ichor creates, every new hardening is a typed option that is off by default, for new and existing clusters alike; disk encryption stays opt-in (the user's decision, 2026-11-02). Each area below records its decisions and what was built.

## Context

The user asked for security to be hardened "to the extreme" (2026-11-02). Five audits read the code and manifests and listed findings with file:line evidence, an exploit scenario and a fix. Asked how far new clusters' defaults should go, the user chose that all cluster-side hardening is opt-in, and that disk encryption stays opt-in.

## Admission and confused deputy

## ichor-ui and its auth

## Credentials and the ichor-ui identity

## Secrets handling

## Supply chain and workloads

## Typed hardening options for managed clusters
