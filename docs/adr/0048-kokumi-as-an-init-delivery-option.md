# 0048. Kokumi is an option of `ichor init`'s Argo CD hand-off, designed now and built later

Status: Accepted (implemented 2026-10-27); superseded in part by ADR 0090 (`ichor images list --kokumi` removed) and in part by ADR 0099 · Date: 2026-10-26 · Design section: GitOps and Argo CD

Decided: `ichor init --argocd --kokumi` prepares the OCI repo-creds Secret, installs neither Kokumi nor Argo CD, and leaves the repository skeleton (including the Cilium Application) unchanged; `ichor images list --kokumi` names Kokumi 0.17.0's images

## Context

Kokumi is offered as an option when installing Argo CD, not as an
`ichor gitops export` target, a PR-mode destination or a third kind of
tracking detection.

Kokumi's own docs (kokumi.dev, github.com/kokumi-dev/kokumi) describe an OCI-first delivery operator, Apache-2.0, "currently
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

## Open questions at design time

- (Partly answered, ADR 0055: in Kokumi's source at commit 4126144 the
  Application is hard-coded with prune, selfHeal and `ServerSideApply=true`,
  has no `ignoreDifferences`, and edits to it are reverted. No sync-wave or
  other options are passed. Still to confirm.)
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
- The Argo CD version range: Kokumi says >= 3.3; ADR 0019 targets v3.5.x
  shapes. Likely compatible.
- Kokumi's project status is "experimental"; its API is `v1alpha1`.

## Consequences

- Without `--kokumi`, `init --argocd` behaves as ADR 0019 describes.
- `ichor images list --kokumi` prints Kokumi's images (see Implementation); it needed a section only if Kokumi's
  images are shown to be mirrorable through the existing path.

## Implementation (2026-10-27)

Accepted and built, in the narrow form the Decision above describes.

- Source: github.com/kokumi-dev/kokumi, release 0.17.0 (2026-10-11,
  latest at the time), one asset `install.yaml` (sha256
  `345223726800ca3f08f633c76c91a38265bc0c131551b24dc49c69ee0cd7b840`). Its CRDs
  are in `delivery.kokumi.dev` (Approval, Kitchen, Menu, Order, Pantry,
  Preparation, Recipe, Serving); everything runs in namespace `kokumi`. The
  pinned version lives in `internal/cli/kokumi.go` (`kokumiVersion`).
- Flag: `ichor init --argocd --kokumi` (`--kokumi` without `--argocd` is
  refused before anything happens). The old `--delivery kokumi` placeholder was
  not used: this is a boolean refinement of `--argocd`, not a choice among
  delivery kinds.
- What it does: creates the Secret `kokumi-registry-repo-creds` in
  `--argocd-namespace` (`argocd.argoproj.io/secret-type: repo-creds`, `type:
  oci`, `url: oci://kokumi-registry.kokumi.svc.cluster.local:5000`,
  `insecureOCIForceHttp: "true"`). Soft-fail like the repository Secret. It prints
  that Kokumi was not installed.
- What it does not do: the repository skeleton is byte-identical with and
  without `--kokumi`. Ichor writes no Kokumi CRs (Kitchen, Menu, Order,
  Serving...) and moves none of its own CRs behind Kokumi. This is the
  conservative answer to the root-Application coexistence unknown: the root
  Application keeps delivering everything Ichor generates.
- Cilium: the Cilium Application stays a plain Argo CD Application in the
  repository (`apps/cni/application.yaml`, delivered by the root Application),
  so it keeps `ignoreDifferences` and `ServerSideApply=true` (ADR 0055). Kokumi
  never delivers Cilium, which removes the open question rather than answering
  the `argocd-cm` route. A user who delivers other things through Kokumi is
  unaffected.
- Air gap: Kokumi 0.17.0's install.yaml runs two images, listed by `ichor images
  list --kokumi` under `kokumiImages` with their upstream registries (not
  rewritten by `--image-registry`): `ghcr.io/kokumi-dev/kokumi:0.17.0` (operator
  and server) and `docker.io/library/registry:3.1.2@sha256:ddf75434...`. Images
  referenced in Orders/Recipes at runtime are the user's and not listed.
- `gitops export` and `gitops handoff` are unchanged: Kokumi-created
  Applications are ordinary Argo CD Applications and tracking detection is
  untouched.

Deferred: delivering any Ichor CR through Kokumi; the Argo CD >= 3.3 check and
Kubernetes >= 1.26 check; a TLS or authenticated Kokumi registry (the Secret assumes the
bundled plain-HTTP one); tracking Kokumi upgrades past 0.17.0 (v1alpha1,
experimental).
