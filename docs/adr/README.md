# Decision records

One file per decision, `NNNN-short-title.md`, based on `0000-template.md`.
Write one when you pick something the design doesn't pin down: a default, a
library, a metric name, a workaround. Keep it short. Don't rewrite accepted
records; supersede them.

The table below is generated from each file's `Decided:`/`Status:` header
line by `internal/tools/adrindex` (`make adr-index`) -- never hand-edit it.
Give a new ADR a `Decided:` line (`0000-template.md` shows where) and
regenerate; CI's generated-artifact drift check catches a stale table the
same way it catches a stale chart.

| # | Decided | Status |
|---|---|---|
| 0001 | Record decisions as one-file-per-decision ADRs, linked from the PR that makes them | Accepted |
| 0002 | Pinned `nixpkgs` to `nixpkgs-unstable` and committed `flake.lock` for a reproducible shell | Accepted, amended |
| 0003 | Generate the Talos machinery mirror types instead of hand-maintaining a parallel field catalogue | Accepted |
| 0004 | Phase 1 scaffolds only `TalosCluster`, `TalosMachine` and `TalosConfigPatch`, with no controllers yet | Accepted |
| 0005 | Moved controller-gen, kustomize, setup-envtest and golangci-lint out of `flake.nix` and into Makefile-managed `./bin` | Accepted |
| 0006 | Run `cert-controller` in-process for the webhook serving certificate instead of depending on cert-manager | Accepted |
| 0007 | e2e runs Talos nodes as KubeVirt VMs on a real cluster, not `talosctl cluster create qemu` | Accepted |
| 0008 | `controlPlane.schedulable` takes a required `true`/`false`; dropped `auto` | Accepted |
| 0009 | Keep predicting reboot necessity from `pkg/fieldmeta`'s field-path table; Talos itself won't answer the question | Accepted |
| 0010 | Added `CheckControlPlaneQuorum` as a pure guard stronger than the `allow-delete` annotation alone | Accepted |
| 0011 | Made `TalosMachineSpec.Role` optional; discovery never picks a role or auto-registers a weak identity | Accepted |
| 0012 | Mirror `VolumeConfig`/`UserVolumeConfig`/`RawVolumeConfig` as three more mirrorgen roots, for disk roles | Accepted |
| 0013 | Settled the `cni.Preset` interface, `custom`'s required fields, Hubble's certificate handling, and where the two immutability guards live | Accepted |
| 0014 | Settled `cmd/ichor`'s package layout and how `ichor adopt` guarantees zero drift | Accepted |
| 0015 | Built GitOps `Application` as a hand-rolled type (`pkg/gitops`) behind one constructor, plus a policy-free repo skeleton | Accepted |
| 0016 | Split the Connect read/approval API into `ClusterService`/`MachineService`/`NodeService` and fixed their message shapes | Accepted |
| 0017 | Settled `ui/`'s directory, `cmd/ichor-ui` as a second binary, and how the Go side builds without the UI present | Accepted |
| 0018 | One `Server` type implements all three handler interfaces over a single uncached client; settled the four approval refusals; fixed `loadBundle` resolving the TalosCluster's own namespace instead of the backend's, the same bug ADR 0011 had already fixed on the controller side; granted the `nodes` get/list `findKubernetesNode` had always read without it; added a chart-vs-`config/` RBAC drift test after the chart's own copy of this `ClusterRole` was found silently narrower than `config/rbac/ichor-ui/role.yaml` | Accepted, amended four times |
| 0019 | `ichor init`: VIP recommended not required, Ichor applied through the API server not inlined, topology presets carry count/schedulable | Accepted |
| 0020 | Gave `ichor-ui` a Dockerfile, a chart Deployment/Service reusing the controller's own mechanism, and RBAC wired into `config/default` | Accepted, amended |
| 0021 | Built the four read/approval UI screens on a committed, generated TS client, with the approval dialog disclosing identity/hardware/role before use | Accepted, amended twice |
| 0022 | Delegate the Talos/Kubernetes compatibility matrix to `pkg/machinery/compatibility` instead of reimplementing it | Accepted |
| 0023 | Keep `TalosStorage` to layer 1 only -- a `longhorn-v1` preset emitting the Talos-side config a node needs, never the storage system itself | Accepted |
| 0024 | No CNI hand-over registry (one real member); removal happens at render time off a status condition; a separate controller | Accepted |
| 0025 | Layer 4's storage-system side: `EvacuationHook` (write side) plus a preset-independent `EvacuationStatus` function (read side), both verified against longhorn-manager v1.9.1's own controller source | Accepted |
| 0026 | `TalosOperation`: the backbone every upgrade kind shares (lock, status-held state machine, pause-on-failure, paused, maxUnavailable, hold annotation, ordering), with none of their execution | Accepted |
| 0027 | Private registry auth through `TalosCluster.spec.registries[].authSecretRef`, resolved and injected as the system layer by `pkg/render` | Accepted |
| 0028 | `ichor gitops export`'s allow-list of CR kinds, its Argo CD/Flux tracked-resource warning, and `ichor gitops handoff`'s one-field `TalosCluster` patch | Accepted |
| 0029 | Generate `docs/adr/README.md`'s index table from a `Decided:`/`Status:` line in each ADR's own header (`internal/tools/adrindex`) instead of a shared, hand-edited table | Accepted |
| 0030 | `ichor ui` discovers ichor-ui pods through the `ichor-ui-service` Service's own selector/port and reforwards to another ready pod (same local port) whenever the tunnel ends, instead of targeting one pod name | Accepted |
| 0031 | made `config/samples/*.yaml` minimal, valid and apply-tested instead of deleting them; gave the metrics endpoint a real, opt-in certificate named `metricsCert.enabled` (not `certManager`, and not the default) via a second cert-controller rotator instead of leaving `insecureSkipVerify`'s documented escape hatch wired to nothing; confirmed the webhook certificate has the same cold-start race the metrics one does; and logged that race instead of papering over it with a bootstrap certificate, which would trade a loud failure for a silent one. | Accepted, amended twice |
| 0032 | Complete a TalosMachine deletion by removing its etcd member -- self-leave first, a survivor removing it by ID as fallback -- clearing the finalizer only after that succeeds, and warning that the node keeps the cluster's credentials until it is reset | Accepted |
| 0033 | moved the identity-tuple rule to `TalosMachineIdentity.IsStrongIdentity` in `api/v1alpha1`, and the secrets-bundle Secret read/decode to a new `internal/secretsbundle` package, so `internal/controller` and `internal/uiapi` each still call it from their own, separately-justified call site, but neither keeps its own copy of the rule. | Accepted |
| 0034 | built the one piece of phase 6's "config editing by layer ... effective-config view annotated by origin and secrets masked" that the current API surface actually supports — grouping `GetEffectiveConfig`'s values by `origin.layer` in the design's own bottom-up order — and did not build layer *editing*, impact preview, or schema-driven forms, because none of them have a backend to call yet. | Accepted |
| 0035 | built `PatchService` (CRUD for `TalosConfigPatch` plus `PreviewImpact`) exactly as ADR 0034's gap report proposed it, with the opaque-JSON `PatchDocument` shape that avoids re-modeling Talos fields a third time, and with no new RPC for schema-driven forms since the CRD's own committed OpenAPI schema already serves that purpose. | Accepted |
| 0036 | `ichor images list` prints the five fixed Image Factory images (version-gated) plus an unconditional warning naming the two catalogs, instead of trying to resolve a schematic's extensions/overlays into images | Accepted |
| 0037 | built `pkg/prmode`'s `ClassifySource`/`Locate`/`Patch`/`Diff` -- finding a tracked CR's file via its Application's source and editing one field with go-yaml's `yaml.Node`, never a PR host API -- as its own host-independent, fully unit-testable unit, separate from opening a PR | Accepted |
| 0038 | `pkg/prmode` now clones a repository, pushes a branch via the `git` CLI (never `go-git`), and opens a PR/MR via one `net/http` POST per host (never a GitHub/GitLab/Gitea SDK) -- with the token carried only through `git`'s own `GIT_CONFIG_*` environment-variable mechanism and an HTTP request header, never argv, a URL, or a log line | Accepted |
| 0039 | built the `PatchService` CRUD screens (list, create, edit, delete) and an explicit impact-preview action on `ui/src/pages/PatchList.tsx`/`PatchDetail.tsx`, editing each `PatchDocument` and `LabelSelector` as a JSON blob in a `Textarea` rather than through a generic schema-driven form renderer, and relying on the apiserver's own CRD validation (surfaced via `describeError`) instead of a second, hand-written validation layer. | Accepted |
| 0041 | redact bundle material from dry-run warnings unconditionally rather than relying on Talos v1.14.3's current (verified) behavior of never echoing one; renamed a registry-auth leak test to match what it actually exercises and documented its two genuinely unreachable paths; validate `ichor init --cluster` as a Kubernetes object name before any file write; added CEL format checks to `TalosClusterRegistry.host`/`.endpoints` since machinery validates neither. | Accepted |
| 0042 | Add an opt-in `ichor.maesh.dev/reset-on-delete` annotation that, after a removal's etcd member is gone, wipes only STATE and EPHEMERAL by label and reboots the node to maintenance mode -- holding the finalizer if Reset fails, and recording the request only after Talos has acknowledged it, so a resumed reconcile never asks twice | Accepted |
