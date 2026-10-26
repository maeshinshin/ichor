# 0048. Kokumi is an option of `ichor init`'s Argo CD hand-off, designed now and built later

Status: Proposed · Date: 2026-10-26 · Design section: GitOps and Argo CD

Decided: Kokumi is selectable in `ichor init`'s Argo CD setup (prepares the OCI repo-creds Secret, installs neither Kokumi nor Argo CD); recorded as design only, implementation deferred

## Context

A user asked for Kokumi to be usable with Ichor, specifically "as an option
when installing Argo CD", not as an `ichor gitops export` target, a PR-mode
destination or a third kind of tracking detection.

Verified from Kokumi's own docs (kokumi.dev, github.com/kokumi-dev/kokumi,
read 2026-10-26): an OCI-first delivery operator, Apache-2.0, "currently
experimental". Renders become immutable content-addressed OCI artifacts
(Preparations); a Serving creates or updates an Argo CD `Application` in the
`argocd` namespace with `spec.source.repoURL` set to the Preparation's OCI
artifact and `targetRevision` to its digest. It requires Argo CD >= 3.3
installed first and Kubernetes >= 1.26. It installs by `kubectl apply -f`
of a release `install.yaml`. Argo CD needs a repo-creds Secret
(`argocd.argoproj.io/secret-type: repo-creds`, `type: oci`, `url:
oci://kokumi-registry.kokumi.svc.cluster.local:5000`,
`insecureOCIForceHttp: "true"` because the bundled registry serves plain
HTTP). Kokumi's Applications have no resources-finalizer.

## Decision

Design only (architecture.md, "Kokumi as the delivery layer in front of
Argo CD"):

- `init` offers Kokumi as a choice of the Argo CD hand-off. It prepares the
  repo-creds Secret from the CLI (never into Git) and installs neither Argo
  CD nor Kokumi, consistent with ADR 0019: fetching and trusting a
  third-party release during a privileged bootstrap is not `init`'s call.
- Ichor's tracking detection, Flux handling and `argocd-cm` reading are
  unchanged.
- Flag name (`--delivery kokumi` is a placeholder) is left to implementation.

## Why implementation is deferred

Reconciling the design against the code turned up gaps that matter more than
a new option, and they should be filled first so Kokumi is not built on top
of them. Adding a delivery layer before those are closed would widen the
surface that has to be re-verified.

## Unknowns (what to check before building)

- Whether Kokumi's Application can be given a `ref`/path or only a bare
  artifact, and whether its Applications can carry Ichor's sync-wave and
  sync options. Check: Kokumi's Serving CRD reference and a generated
  Application on a test cluster.
- How the root Application (source: a repository) coexists with
  Kokumi-created Applications, and which Ichor CRs would be delivered as
  Products/Orders. Check: Kokumi's Product/Order schema and
  "Producing Base Artifacts" guide. Not in the pages read.
- Air gap: the images Kokumi itself and its scanner need, and how to mirror
  its artifacts. Check: Kokumi's Container Images (operator guide) and
  Registries guides; the pages read only listed these topics.
- The Argo CD version range: Kokumi says >= 3.3; ADR 0019 verified shapes
  against v3.5.x. Likely compatible, not tested.
- Kokumi's project status is "experimental"; its API is `v1alpha1`.

## Consequences

- No code changes now; `init --argocd` behaves as ADR 0019 describes.
- When built, `ichor images list` needs a Kokumi section only if Kokumi's
  images are shown to be mirrorable through the existing path.
