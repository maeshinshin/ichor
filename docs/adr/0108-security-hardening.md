# 0108. Security hardening

Status: Accepted; in progress · Date: 2026-11-02 · Design section: Trust, Deployment, Nodes · Touches: 0011, 0041, 0058, 0063, 0089, 0103, 0105, 0107

Decided: Ichor is hardened area by area after five read-only audits (2026-11-02): ichor-ui and its auth, the controller's privileges and admission, workloads and the supply chain, the security of the clusters Ichor creates, and secrets and credentials. Fixes to Ichor itself are made directly. For the clusters Ichor creates, every new hardening is a typed option that is off by default, for new and existing clusters alike; disk encryption stays opt-in (the user's decision, 2026-11-02). Each area below records its decisions and what was built.

## Context

The user asked for security to be hardened "to the extreme" (2026-11-02). Five audits read the code and manifests and listed findings with file:line evidence, an exploit scenario and a fix. Asked how far new clusters' defaults should go, the user chose that all cluster-side hardening is opt-in, and that disk encryption stays opt-in.

## Admission and confused deputy

## ichor-ui and its auth

Decided and built (audit findings F2 to F18 of the ichor-ui audit; F1, the impersonation scope, is under "Credentials and the ichor-ui identity").

- **Request sizes (F2).** `connect.WithReadMaxBytes` on every handler: 256 KiB, and 1 MiB for the services that carry pasted documents (patches, Cilium values, reclaim's talosconfig). The limit applies to the decompressed message and is checked before the interceptors, so an unauthenticated caller cannot make the pod allocate. The mux caps any body at 2 MiB, the issuer's endpoints at 64 KiB, the internal listener at 4 KiB. Every RPC takes one request message (the streaming ones are server streams), so the limits do not touch streaming.
- **Timeouts (F5).** Main listener: `ReadHeaderTimeout` 10 s, `ReadTimeout` 60 s, `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB, and no `WriteTimeout` because `StreamNodeLogs` and the downloads run for minutes; a request body is read within `ReadTimeout`, and a stream is bounded by its token (below). Internal listener (kube-apiserver only): 5 s, 15 s, 15 s write, 60 s idle, 16 KiB headers.
- **Security headers (F4).** One middleware on the whole listener, issuer pages included: CSP (`default-src 'none'`, `script-src 'self'`, `connect-src 'self'`, `img-src 'self' data:`, `font-src 'self'`, `base-uri 'none'`, `form-action 'self'`, `frame-ancestors 'none'`), HSTS one year without preload, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`, a minimal `Permissions-Policy`, `Cross-Origin-Opener-Policy: same-origin`. The theme snippet moved from `index.html` to `ui/public/theme-init.js`, so scripts need no `'unsafe-inline'`. **`style-src` keeps `'unsafe-inline'`**: two stock pieces create a `<style>` element at runtime (the Radix select viewport and the sonner shim), and editing preactcn components was ruled out; inline CSS cannot run script, so the residual risk is CSS injection only. With an external OIDC provider the browser itself fetches the provider's discovery document and token endpoint, whose origins are not known to ichor-ui, so `connect-src` widens to `'self' https:` in that case only; scripts stay `'self'`.
- **Token revocation (F6).** `authenticateLocal` compares the token's `iat` with the credential's `notBefore` (same cached read as `mustChange`, 10 s), so RevokeSessions and a password change end tokens already issued, not only refresh grants. `notBefore` has whole-second resolution: a token issued in the same second as the revoke survives it. The UI signs out after its own password change. The kube-apiserver cannot do this check; a token already given to `kubectl` lives out its lifetime.
- **Refresh rotation (F7).** A refresh grant returns a new refresh token and retires the old. State is one entry per live family (`families` in the user's credentials entry, so all replicas agree and RevokeSessions or a password change drops them): the id of the current token and the family's expiry, at most 16 per user. Writes are optimistic-lock patches of the Secret, retried on conflict. Presenting a retired token deletes the family (the owner signs in again) and logs it. Rotation keeps the session's start and expiry, so the absolute lifetime is not extended. A token from before rotation is accepted once and starts a family. A race between two processes refreshing the same token is serialised by the CLI's file lock, so it does not trip reuse detection.
- **Login limits (F8, F9, F10, F11).** Failures are counted per (source, user), so a stranger cannot lock `admin` out; per source with a looser limit (30 free failures, at most 1 min); and per user across sources, where 30 failures in an hour add a 2 s delay to every sign-in of that user and never refuse one. Tables hold 50 000 keys and evict the entries with the fewest failures first, so flooding fresh names cannot push out a slowed key. The source is the TCP peer, or with `--trusted-proxy-cidrs` the rightmost `X-Forwarded-For` address outside the trusted list. Counters stay per replica (a Lease per attempt was too expensive): N replicas give N times the free guesses. `/oidc/authorize`, `/login` and `/token` have a per-source token bucket (20, 10 and 20 per second, bursts 100, 50 and 100). A ticket buys one successful login (a typo may retry). Logins hash on their own argon2 slot with a queue of 8 and answer 503 beyond it; password operations of a signed-in user have another slot, so one cannot starve the other (the memory is the same two slots as before).
- **Role grants (F12).** CreateUser and UpdateUser refuse to grant a role the caller does not hold, unless the caller is admin. This is in code, not a ValidatingAdmissionPolicy; an external (OIDC) identity has no Ichor roles and is still gated by RBAC alone.
- **Streams (F13).** A stream gets a deadline at its token's expiry and ends with Unauthenticated ("your sign-in expired"). A disable inside the token lifetime is not seen by a running stream.
- **Errors (F14).** Internal, unknown, data-loss and unavailable errors reach the browser as the code plus a reference; the full error is logged with that reference. Errors the caller caused are unchanged.
- **/metrics (F15).** It stays on the main port but needs a Kubernetes token that may `get` the non-resource URL `/metrics` (the TokenReview and SubjectAccessReview of the controller's secure endpoint). The ichor-ui role gains `create` on `tokenreviews` and `subjectaccessreviews`; the ServiceMonitors send the pod's token; the scraping Prometheus needs the `metrics-reader` ClusterRole.
- **TLS (F16).** TLS 1.2 or newer, with only ECDHE and AES-GCM or ChaCha20 suites on 1.2 (1.3 suites are fixed by Go), curves X25519 and P-256, on both listeners. The `certreload` mutex is unchanged.
- **Dependencies and keys (F17, F18).** `oras-go` v2.6.2 (govulncheck: no vulnerability reached); `oidctrust` refuses an RSA signing key under 2048 bits.
- **No sign-in (F3).** With neither OIDC nor Ichor users, ichor-ui serves only the procedures in `readOnlyProcedures` (a new RPC is closed until listed; downloads are not listed). `--allow-unauthenticated` (`ICHOR_ALLOW_UNAUTHENTICATED`, chart `ichorUI.allowUnauthenticated`) restores the open mode explicitly; `/auth/config` says `anonymous: readonly|open` and the UI banner shows which. Compatibility: the installer reads a live ichor-ui with no users, no OIDC and no flag as "ran open" and sets the flag with a `NOTICE`, so an upgrade changes nothing; `ichor init` and `ichor adopt` start ichor-ui with the flag, `ichor init` without `--ui-url` prints a warning at the end, and `--ui-url` drops the flag. A Helm upgrade of such an install turns read-only until `ichorUI.allowUnauthenticated=true` is set (the chart prints a note). Making users the `ichor init` default was left out of scope.

Not done here: shared login counters across replicas (documented per replica), a ValidatingAdmissionPolicy for role grants, re-checking a disabled user inside a running stream before its token ends, narrowing the Role's `secrets: get` to names (F19, with the credentials work).

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
