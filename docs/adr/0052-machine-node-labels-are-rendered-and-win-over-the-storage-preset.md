# 0052. `TalosMachine.spec.nodeLabels` is rendered, wins over the `TalosStorage` preset, and a kubelet-forbidden label is a render error

Status: Accepted · Date: 2026-10-26 · Design section: Storage, Patch layers
Decided: Render `spec.nodeLabels` into `KubeNodeConfig` (contract 1.14+) or `machine.nodeLabels` (older) right after layer 1, so the operator's label beats a `TalosStorage` preset label on the same key, and fail the render for labels the kubelet's NodeRestriction would refuse

## Context

Approval sets `spec.nodeLabels` (ADR 0004, 0011), but nothing rendered them, so an approved label
never reached the Node. ADR 0050 made `TalosStorage` selection match on `spec.nodeLabels` at
render time and on the live Node's labels at evacuation time; with the labels never applied,
layer 1 and layer 4 disagreed. That gap, noted in ADR 0050 decision 2, is closed here.

## Decisions

**1. Where.** `injectNodeLabels` (`pkg/render/nodelabels.go`) merges the labels with the same
version-contract branch as `nodeRegistrationDocs`: `KubeNodeConfig.labels` from Talos 1.14,
legacy `machine.nodeLabels` below it (a legacy block next to the multi-document base fails
validation).

**2. Precedence.** It runs after `injectStorage` and before every `TalosConfigPatch`.
`spec.nodeLabels` is the operator's intent (machine layer); the preset's labels are system
layer. On a shared key the machine's value wins (the strategic merge applies it last), so an
operator can set `node.longhorn.io/create-default-disk: "false"` on one node. A patch still
beats both.

**3. Forbidden labels.** Talos v1.14.3 validates only syntax (`pkg/machinery/labels`:
qualified name, value of at most 63 characters) and has no reserved list of its own; its own
field comment points at the kube-apiserver NodeRestriction plugin. A kubelet started with
`node-role.kubernetes.io/...` or another `kubernetes.io` / `k8s.io` label outside the allowed
set refuses to start. Render therefore returns an error naming the label (allowed:
`kubernetes.io/{hostname,arch,os}`, the topology / instance-type / failure-domain keys, and
anything under `kubelet.kubernetes.io` or `node.kubernetes.io`). Not a CEL rule: the allowed
set is Kubernetes', not ours, and duplicating it into the CRD would drift; the error surfaces
as the machine's render failure condition.
