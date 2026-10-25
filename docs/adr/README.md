# Decision records

One file per decision, `NNNN-short-title.md`, based on `0000-template.md`.
Write one when you pick something the design doesn't pin down: a default, a
library, a metric name, a workaround. Keep it short. Don't rewrite accepted
records; supersede them.

The table below must list every file in this directory (`0000-template.md`
excepted) and no others, with no gaps in the numbering; `index_test.go`
checks both and fails naming the exact mismatch or missing number. Add a
row here in the same PR that adds the ADR.

| # | Decided | Status |
|---|---|---|
| 0001 | Record decisions as one-file-per-decision ADRs, linked from the PR that makes them | Accepted |
| 0002 | Pinned `nixpkgs` to `nixpkgs-unstable` and committed `flake.lock` for a reproducible shell | Accepted, amended |
| 0003 | Generate the Talos machinery mirror types instead of hand-maintaining a parallel field catalogue | Accepted |
| 0004 | Phase 1 scaffolds only `TalosCluster`, `TalosMachine` and `TalosConfigPatch`, with no controllers yet | Accepted |
| 0005 | Moved controller-gen, kustomize, setup-envtest and golangci-lint out of `flake.nix` and into Makefile-managed `./bin` | Accepted |
| 0006 | Run `cert-controller` in-process for the webhook serving certificate instead of depending on cert-manager | Accepted |
| 0007 | e2e runs Talos nodes on a real cluster, not `talosctl cluster create qemu` | Accepted |
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
| 0018 | One `Server` type implements all three handler interfaces over a single uncached client; settled the four approval refusals | Accepted, amended |
| 0019 | `ichor init`: VIP recommended not required, Ichor applied through the API server not inlined, topology presets carry count/schedulable | Accepted |
| 0020 | Gave `ichor-ui` a Dockerfile, a chart Deployment/Service reusing the controller's own mechanism, and RBAC wired into `config/default` | Accepted, amended |
| 0021 | Built the four read/approval UI screens on a committed, generated TS client, with the approval dialog disclosing identity/hardware/role before use | Accepted, amended twice |
| 0022 | Delegate the Talos/Kubernetes compatibility matrix to `pkg/machinery/compatibility` instead of reimplementing it | Accepted |
| 0023 | Keep `TalosStorage` to layer 1 only -- a `longhorn-v1` preset emitting the Talos-side config a node needs, never the storage system itself | Accepted |
| 0024 | No CNI hand-over registry (one real member); removal happens at render time off a status condition; a separate controller | Accepted |
