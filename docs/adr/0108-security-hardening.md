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

Audit: workload hardening and the supply chain. Fixed here, all of it without a CRD change.

### Decisions

- One release path. The images that ship are built by `test/harness/ui-demo/images.sh` (crane, no container
  runtime), as ADR 0104 unit 7 already had it. The Dockerfiles are for local `docker build` and carry the same
  digests and flags, so there is no second release path to drift. `release-images.yml` and `release-chart.yml` are
  replaced by one `release.yml`.
- Reproducible and pinned. The distroless base, `golang` and `node` images are pinned by digest (the tag stays
  in front for Renovate). Binaries build with `-trimpath` and `-ldflags "-s -w -X ...Version"`; the layer tar has
  fixed owner, mtime and order; the push uses `--set-base-image-annotations`. Pushing the same binaries twice gives
  the same digests (checked locally). goreleaser stamps `internal/version.Version` (it set `main.version`, which
  nothing reads).
- Release gating and split. A `v*` tag runs `verify` (module drift, lint, tests, UI CI, govulncheck, npm audit),
  then `build` (dependencies fetched, `contents: read`, no registry token), then `publish` in the `release`
  environment, the only job with `packages: write` and `id-token: write`. `publish` runs crane, cosign, syft and
  the attest actions and fetches no Go or npm dependency.
- Signing and attestation. Keyless cosign (GitHub OIDC) signs the three images and the OCI chart by digest; syft
  writes an SPDX SBOM per image, attested with `actions/attest-sbom`; `actions/attest-build-provenance` attests
  images and chart. Every new action is pinned by commit SHA. Verification is documented in
  `docs/guides/install-with-helm.md`. Ichor does not verify a signature before it runs the installer Job; digest
  resolution of the installer image (`spec.installerDigest`, audit M3) is deferred.
- CI scanning and updates. `govulncheck ./...` and `npm audit --omit=dev --audit-level=high` run on every PR and
  tag (oras-go is bumped to v2.6.2 for GO-2026-6497 and GO-2026-6499). `renovate.json` covers gomod, npm, GitHub
  Actions and Dockerfiles with digest pinning, the images.sh base, and the Nix flake lock; grouped, three open
  PRs at most, Monday mornings, a three-day release age. Renovate rather than dependabot because it handles Nix and
  digests. `trivy` and OSSF scorecard from the audit are not added (govulncheck and npm audit cover the
  dependencies; the images are distroless static).
- Pod Security Admission. The install manifest labels `ichor-system` `enforce`, `audit` and `warn` at
  `restricted` (enforce pinned to v1.32, the others `latest`). Every Ichor pod passes: non-root, seccomp
  RuntimeDefault, no privilege escalation, all capabilities dropped, only emptyDir, secret and projected volumes,
  no host namespaces. The Cilium Helm Job needs `hostNetwork` and runs in `kube-system`, so it is outside. The
  chart does not own the namespace; NOTES.txt and the guide give the `kubectl label` command.
- Pods and Jobs. Controller and UI run as 65532:65532 and under a new PriorityClass `ichor-critical` (value
  1000000, below every `system-*` class, which a user namespace cannot use), shipped in the manifest and the chart
  (`priorityClass.create`). Installer and Cilium Jobs name user and group 65532 and carry requests 50m/64Mi and
  limits 1 CPU/512Mi. The service-account token stays mounted: both pods and the Jobs need the API. The installer
  ClusterRole gets `scheduling.k8s.io/priorityclasses` create/get/patch/update (the manifest now contains one).
- Replicas and PDB. The raw manifest stays at one replica each and ships no PDB (a `minAvailable: 1` budget on one
  replica blocks node drains); the chart runs two and keeps its PDBs. Documented, not changed.
- Network. The Cilium default stays opt-in. The chart's policies gain `externalCIDRs`, `talosCIDRs` and
  `uiIngressCIDRs`; when set they replace the `world` entity for those rules. The installer's static copy keeps
  `world`, because it has no values. FQDN rules and a metrics-namespace restriction are deferred.
- Workflow hygiene. `go mod tidy` no longer rewrites files in CI (`go mod tidy -diff` and `go mod verify` fail on
  drift); kind is pinned by version and sha256; every job has `timeout-minutes`; `test-chart` no longer runs on
  every branch push; `actionlint` (in the Nix shell) runs in the lint workflow; `npm ci --ignore-scripts` in the
  Makefile and `Dockerfile.ichor-ui` (the UI build and tests pass without install scripts).

### As built

- `Dockerfile`, `Dockerfile.ichor-ui`, `Dockerfile.installer`, `test/harness/ui-demo/images.sh` (modes `build` and
  `push`, `DIGEST_FILE`), Makefile targets `demo-images-build` and `demo-images-push`, `.goreleaser.yaml`.
- `.github/workflows/release.yml` (new; the two old release workflows are gone), `test.yml`, `test-e2e.yml`,
  `test-chart.yml`, `lint.yml`, `pr-title.yml`, `renovate.json`, `flake.nix` (cosign, syft, actionlint).
- `config/manager/manager.yaml` (namespace labels, PriorityClass), `config/manager-ui/manager.yaml`,
  `config/rbac/ichor-installer/role.yaml`, `internal/controller/job.go` with `job_test.go`.
- Chart: `values.yaml`, `templates/extras/priority-class.yaml`, `templates/network-policy/cilium.yaml`,
  `templates/rbac/ichor-installer.yaml`, `NOTES.txt`, `hardening_test.go`.
- Not verifiable without GitHub or a cluster: the whole `release.yml` run (cosign, attest, environment approval),
  Renovate's reading of `renovate.json`, and a server-side PSA dry-run against a live cluster. The restricted
  profile was checked by reading the pod specs.
- Maintainer steps in the repository settings (not in files): create the `release` environment with required
  reviewers, and a tag ruleset for `v*`.
- ADR 0104 unit 7 named the two removed workflows; `release.yml` replaces them.

## Typed hardening options for managed clusters
