# 0108. Security hardening

Status: Accepted; in progress · Date: 2026-11-02 · Design section: Trust, Deployment, Nodes · Touches: 0011, 0041, 0058, 0063, 0089, 0103, 0105, 0107

Decided: Ichor is hardened area by area after five read-only audits (2026-11-02): ichor-ui and its auth, the controller's privileges and admission, workloads and the supply chain, the security of the clusters Ichor creates, and secrets and credentials. Fixes to Ichor itself are made directly. For the clusters Ichor creates, every new hardening is a typed option that is off by default, for new and existing clusters alike; disk encryption stays opt-in (the user's decision, 2026-11-02). Each area below records its decisions and what was built.

## Context

The user asked for security to be hardened "to the extreme" (2026-11-02). Five audits read the code and manifests and listed findings with file:line evidence, an exploit scenario and a fix. Asked how far new clusters' defaults should go, the user chose that all cluster-side hardening is opt-in, and that disk encryption stays opt-in.

## Admission and confused deputy

## ichor-ui and its auth

## Credentials and the ichor-ui identity

## Secrets handling

- **Restore marker (S7).** The marker in the backup bucket is stored as `{"marker": ..., "mac": ...}`. The MAC is
  HMAC-SHA256 over the marker JSON, keyed by HKDF over the backup passphrase with info
  `ichor.maesh.dev/etcd-backup/marker/v1`, so it follows whatever key material the backup passphrase derives from.
  A signature mismatch is an error. A legacy unsigned marker is accepted with a logged warning and re-signed by the
  next write: refusing it would strand a restore that is half done across an upgrade, since the marker is the only
  record of how far the destructive part got. The cost is that an attacker with bucket write access can still strip the
  signature; refusing unsigned markers is planned for the release after this one. Snapshot rollback by replaying an
  older valid snapshot is not addressed here.
- **Scrubber (S8).** `bundleSecretMaterial` is derived by reflection over every exported string and `[]byte` field of
  the Talos secrets bundle (trimmed, so a PEM that lost its trailing newline still matches), so keys the bundle gains
  or that were missing (etcd, aggregator and service-account keys, encryption secrets) are redacted. Rotation CAs were
  already added by the rotation code. Base64 re-encodings are not scrubbed.
- **Local files (S9).** `saveKubeconfigFile` now chmods an existing directory to 0700 like `saveBundleFile`. The local
  `secrets.yaml` stays plaintext (0600, atomic); encrypting it is deferred, but `loadBundleFile` now warns on stderr when
  the file is group or world accessible. Downloaded etcd snapshots were already written 0600; the CLI now states that
  they are not encrypted.
- **Reclaim Secret name (S10).** Uses `crypto/rand` (`rand.Text`, lowercased, 16 characters) instead of the
  `math/rand` based `utilrand`.
- **Other (S11).** The manager logs in production mode (JSON, no stack traces on warn); `--zap-devel` turns development
  mode back on, other zap flags are unchanged. The kubetrust trust file is written through a unique `os.CreateTemp`
  file in the same directory and renamed (mode 0644, public CAs only). Refusing `http://` S3 endpoints is left to the
  endpoint gating work.

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

Decided: every hardening of a cluster Ichor creates is a typed field of `TalosCluster.spec.security`, off by default, for new and existing clusters. Unset means the rendered machine config and Cilium values are byte-identical to before (proved by rendering a control plane and a worker from one fixed secrets bundle before and after the change, and by a test that an empty or disabled `spec.security` renders the same bytes). Machine config stays typed: Ichor builds the Talos documents itself, below every patch, and no raw YAML is accepted. `docs/guides/hardening.md` lists every option, what it does and what applying it costs.

- **Firewall** (`security.firewall`). Enabled renders `NetworkDefaultActionConfig` `block` and one `NetworkRuleConfig` per service (`ichor-fw-*`) following the Talos v1.14 recommended control-plane and worker rule sets: apid from the controller, operators and control planes; trustd between nodes; kubelet from control planes and the pod subnets; etcd between control planes; 6443 from the controller, configured sources, nodes and the pod and service subnets; Cilium tunnel (8472, or 6081 for geneve), health (4240) and WireGuard (51871); KubeSpan (51820). `allow` entries open host ports for host-network services. The field set needs the node CIDRs as input because render sees one machine at a time. The preflight is the admission webhook: it refuses a firewall whose `controllerCIDRs` does not contain the address the webhook's pod routes to the endpoint from (a UDP connect, nothing is sent; skipped when the endpoint does not resolve). No reboot (fieldmeta already classes both kinds reboot-free). Not done: applying in Talos `try` mode with automatic revert, which needs a hardware run.
- **WireGuard** (`security.wireGuard`). Sets `encryption.enabled`, `encryption.type: wireguard` and `encryption.nodeEncryption` in the effective Cilium values, after the user layer, so the typed option wins. The existing values hash re-runs the Helm Job; the agents restart in turn, no node reboot. Strict mode is left to `spec.cni.cilium.values`.
- **Pod Security and audit** (`security.podSecurity`, `security.audit`). Merged over Talos's own `KubeAdmissionControlConfig` PodSecurity document, so only the levels set change and `kube-system` stays exempt; audit is one of three presets (`metadata`, `rbacChanges`, `writes`), none logging Secrets or tokens above Metadata. Both need a v1.14 contract and are refused on an older one rather than silently raising it. The kube-apiserver restarts (fieldmeta `apiServerRestart`), no node reboot.
- **Disk encryption** (`security.diskEncryption`: `tpm`, `nodeID`, `kms`). Renders `VolumeConfig` for STATE and EPHEMERAL (LUKS2, slot 0, EPHEMERAL locked to STATE); `static` is not offered; `kms` must be `https`; `requireSecureBoot` maps to the TPM key's `checkSecurebootStatusOnEnroll`. On a cluster with approved machines the webhook refuses enabling or changing it unless the annotation `ichor.maesh.dev/confirm-disk-encryption-wipe: "true"` is present; Ichor never wipes a node, the guide documents the one-node-at-a-time reset-and-add path. Ichor records no TPM inventory, so it cannot pre-check TPM presence; Talos's own enrolment failure is the check (to be confirmed on hardware).
- **Talos API access from pods.** A `TalosConfigPatch` with `machine.features.kubernetesTalosAPIAccess` and `os:admin` in `allowedRoles` is refused unless the user holds the `break-glass` verb on the patch; any enabled access is admitted with a warning. An update that leaves the documents alone is not re-checked. The rule lives in `internal/webhook/v1alpha1/security_options.go`.
- **Not modelled.** SecureBoot and UKI, kernel lockdown, an `os:reader` talosconfig role, kubelet serving-certificate bootstrap: follow-ups.
- **Needs hardware.** That a block-mode firewall with these rules leaves a Talos plus Cilium cluster healthy (including pod to apiserver traffic and DHCP renewal); that the webhook's routed source matches the address Talos sees when the controller runs in a pod; the WireGuard rollout; the apiserver restart for the two Kubernetes documents; the TPM and KMS behaviours.
