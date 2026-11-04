# 0122. The installer image runs by digest and its signature is verified before the installer Job

Status: Accepted · Date: 2026-11-04 · Design section: Upgrades (Ichor), Supply chain · Touches: 0059, 0090, 0104, 0105, 0108

Decided: Before the controller creates the installer Job of an Ichor upgrade it pins the installer image to a digest and verifies that digest's keyless cosign signature, offline against an embedded Sigstore trusted root, for the identity `https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/<spec.version>` issued by GitHub Actions. The digest is `spec.installerDigest` when set, else what the tag `<installerRepository>:<spec.version>` resolves to at that moment (HEAD on the manifest); the Job's image is `<installerRepository>@<digest>`, so what was verified is what runs. The only way not to verify is an explicit, reasoned, recorded opt-out, `spec.installerSignature: {mode: skip, reason: ...}` (`ichor upgrade ichor --skip-signature-check "<reason>"`), which the controller records in `status.installerImage` and an Event and which the CLI and the UI show as a warning. A failed resolution or verification pauses the operation with a reason and creates no Job. The apiserver enforces the digest form for the installer Job through the existing Job-image admission policy. The release workflow verifies the signatures it has just made with the same Go code before it finishes.

## Context

ADR 0108 signs the three release images and the chart keylessly with cosign and documents `cosign verify`, but left two things open (its follow-up table): the installer Job's image is `installerRepository:version`, a tag a registry operator or an attacker with write access can repoint after anyone looked, and nothing in Ichor checks a signature. The installer Job's ServiceAccount holds `escalate` and `bind` on ClusterRoles (ADR 0059), so the installer image is the most privileged thing Ichor runs; "as strong as cluster-admin". Audit finding M3 asked for resolution by digest (`spec.installerDigest`) and a signature check before the Job runs.

`internal/imagesig` and its `sigstore-go` dependency existed until commit 3c04d972 for the Cilium image-patch path ADR 0115 later removed. The verification code, the embedded trusted root and the way the bundles are read (OCI referrers, Sigstore bundle v0.3, what cosign 3 writes by default; `cosign sign` v3.1.3 is the flake's version) are reused here. ADR 0090 says air-gapped installs are out of scope; mirrors are in scope (`installerRepository`).

## Decisions

### 1. Where the digest comes from

Two sources, one rule: the Job never runs a tag.

- `spec.installerDigest` (new, `^sha256:[a-f0-9]{64}$`): a person's pin. The controller does not resolve the tag at all, so a pin plus `skip` needs no registry access from the controller.
- Otherwise the executor resolves `<installerRepository>:<spec.version>` with a manifest HEAD request (`imagesig.ResolveDigest`) when it creates the Job, and uses that digest for both the signature check and the Job. It records `{version, repository, digest, verification, signer|reason, resolvedAt}` in the new `status.installerImage` and puts the digest on the Job (annotation `ichor.maesh.dev/installer-image-digest`). A later reconcile of the same operation reads the Job, never the registry again; a changed `spec.installerDigest` while a Job exists pauses the operation, as a changed `spec.version` does (ADR 0059).

Rejected: a release manifest embedded in the CLI or the controller. The controller that runs the upgrade is the old one and cannot hold a digest of a release that did not exist when it was built; the CLI could only name its own version's digests, tying the CLI's version to the target, and the manifest would itself need a trust root. Resolving at upgrade time against the registry the user chose, then verifying a signature that is bound to the digest, needs neither. Rejected: writing the resolved digest into `spec` (the controller never mutates spec); it goes to `status`.

The resolve and verify happen once per Job, before it exists: a window between "verified" and "pulled" no longer exists because the pull is by digest (this is the point of M3).

### 2. Who signs, and the identity

Keyless cosign with the GitHub Actions OIDC token, which `release.yml` already does for the installer, controller and UI images and the chart (`cosign sign --yes <repo>@<digest>`, in the `publish` job, the only job with `id-token: write`). The certificate's issuer is `https://token.actions.githubusercontent.com` and its subject alternative name is `https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/<tag>`. `imagesig.IchorIdentity(spec.version)` is that identity as an anchored, quoted regular expression, so only a signature made by that workflow on the tag equal to `spec.version` verifies; a valid signature of another tag, another workflow, a branch ref or another repository does not. It is the identity `docs/guides/install-with-helm.md` gives to `cosign verify`.

The identity is not configurable. A fork that builds and signs its own installer image runs with `skip` (recorded) until a decision on a configurable signer is made; making it configurable needs a second trust decision (who may a repository owner name) that this ADR does not take.

A new step in `release.yml`'s `publish` job, after signing, verifies each of the three images and the installer's signature with `ichor`'s own verifier (`internal/tools/verifyimage`, built in the `build` job, which may fetch Go modules, and shipped in the artifact, so `publish` still fetches none). A format or trust-root mismatch between cosign and `sigstore-go` therefore fails the release instead of the first upgrade.

### 3. How verification works offline, with mirrors, and what is trusted

- Offline: the bundle carries the Fulcio certificate, the signed certificate timestamp, the Rekor inclusion proof and entry timestamp, so `sigstore-go` verifies it against `internal/imagesig/trusted_root.json` (the Sigstore public-good root, embedded, no TUF fetch) without contacting Sigstore. The only network traffic is the registry: a manifest HEAD, the referrers listing of the digest and the bundle blob. The trusted root changes with an Ichor release; a signature made after Sigstore rotates its keys may not verify on a controller built before it. The recovery is `skip` with a reason, then the next Ichor release carries the new root. (Rejected: a root fetched at run time, which makes the verifier depend on a second online service; a ConfigMap override, which is a way to widen trust that needs its own review.)
- Mirrors: the signature is about the digest, not the registry. A mirror that carries the image under the same digest and the image's OCI referrers (for example `oras copy --recursive`; a plain `crane copy` or `docker pull/push` does not copy referrers) verifies unchanged. A mirror that carries the image only fails with "has no cosign signature" and the operator has two honest ways out: copy the referrers, or `skip` with a reason. The controller reads the registry anonymously (the Job's own pull uses the node's credentials, as before); a private mirror that needs a pull secret for the controller's reads is `spec.installerDigest` plus `skip` today.
- Egress: the controller's Cilium egress policy (ADR 0100) gets one more dynamic policy, `ichor-controller-installer-image`, maintained by the IchorInstallation reconciler: HTTPS to the host of `spec.installerRepository`, plus `pkg-containers.githubusercontent.com` when that host is `ghcr.io` (GHCR redirects blobs there). It exists whenever Ichor's own controller policy exists, whether or not the update check is on, and is the same shape as the release check's. A registry that redirects blobs to another host needs that host named; until it is, the verification fails with the registry's error and the pause says so (`skip` is the way out).

### 4. The opt-out for unsigned images

Development and demo builds (`v0.0.<N>-g<sha>`, pushed to a mirror by `make demo-images-push`) are not signed. `spec.installerSignature` is `{mode: required|skip, reason}`; `skip` requires a non-empty reason (CEL on the CRD), is a `version`-subresource write (the same right as changing `spec.version`, `installerRepository` and the digest; the settings webhook records who changed it in an Event), and is never inherited: `CreateOperation` for an Ichor upgrade writes `installerDigest` and `installerSignature` exactly as the request says, so a later upgrade without `--skip-signature-check` writes `required` again. A standing skip cannot outlive the upgrade it was given for through the CLI or the UI backend; a person who edits the CR by hand owns what they leave there.

When the controller runs a Job under `skip` it writes `status.installerImage.verification: Skipped` with the reason and the digest, emits a Warning Event on the IchorInstallation (`InstallerSignatureSkipped`) and logs it. Shown, not hidden:

- `ichor upgrade ichor --skip-signature-check "<reason>"` prints, before it asks for confirmation, a warning that the installer image is not verified and why, and `ichor upgrade ichor --installer-digest sha256:...` pins. The plan the UI backend returns carries the same warning.
- The UI shows a destructive badge "Installer image not verified" in the top bar while `spec.installerSignature.mode` is `skip` or the last run was `Skipped`, with the reason and the digest on hover and on the Ichor card of the dashboard.

Rejected: a controller flag or chart value that turns verification off for the whole installation (a silent, standing bypass), a verification mode "warn" (an upgrade that proceeds on a failed check teaches people to ignore it), and treating a dev version string as unsigned automatically (a release-looking tag from a hostile mirror would then pass).

### 5. Failure

The executor returns a typed error; `advanceIchor` pauses the operation (ADR 0026) with the reason `InstallerImageUnresolved` (the tag did not resolve: registry unreachable, tag missing, egress) or `InstallerImageUnverified` (no signature, a signature that does not verify, wrong identity, wrong digest) and the full message. No Job is created, so the installer ServiceAccount does not run, nothing is snapshotted and nothing is applied. Resuming (ADR 0095's retry annotation, `ichor operation retry`) re-runs resolve and verify. Nothing ever falls back to the tag or to unverified.

### 6. The apiserver enforces the digest form

`ichor-controller-job-image` (ADR 0108) now requires, for a container of the `ichor-installer` ServiceAccount's Job, `image == installerRepository + '@' + installerDigest` when `spec.installerDigest` is set, else `installerRepository + '@sha256:<64 chars>'`. A controller that was compromised or buggy and created a tag-form Job is refused by the apiserver. The Cilium Helm Job (`ichor-cni`) keeps `installerRepository:version`: it runs the installer image of the version already installed, under a ServiceAccount that cannot create roles outside its namespace, and its Job exists in Helm-only installs with no IchorInstallation (see Residual). The chart's structural rule for a chart-only install (`admission.jobImageRepository`) accepts both forms.

## Residual risk and what this does not do

- The first upgrade onto a release that contains this change is made by the older controller, which creates the Job by tag as before. Protection starts with the upgrade after that.
- The installer applies a manifest whose controller and UI images are tags, not digests. A signed installer therefore still pulls `ichor-controller:<tag>` and `ichor-ui:<tag>` by tag. Closing that needs the installer image to be built after the other two images are pushed so its embedded manifest names their digests; the build job has no registry access today. Follow-up.
- The Cilium Helm Job's installer image is tag-resolved (above).
- No revocation: a signature stays valid after the key-less certificate expires (that is how keyless works); a compromised release is withdrawn by a new release and the operator's `spec.version`, not by Ichor.
- The trusted root is embedded; see 3.
- The verification reads signatures only from `installerRepository`; a signature published at a different repository is not looked for.

## Built

- `internal/imagesig` (restored, `IchorIdentity`, `ResolveDigest`; the live test for Cilium is gone), `internal/tools/verifyimage`, `release.yml` (verify step, build step for the tool).
- `api/v1alpha1/ichorinstallation_types.go`: `spec.installerDigest`, `spec.installerSignature`, `status.installerImage`; CRD and chart CRD regenerated. Fields only added.
- `internal/controller`: `ichor_upgrade.go` (resolve, verify, digest Job, Job annotation, record, Event), the pause reasons, the egress policy in the IchorInstallation reconciler.
- `config/admission/controller-jobs-policy.yaml` and the chart copy; `internal/webhook/v1alpha1` settings gate (the new fields need the `version` subresource).
- `proto/ichor/v1alpha1/operation.proto` (`IchorInstallerOptions` on plan and create; installer-image fields on `GetIchorInstallationResponse`), `internal/uiapi`, `internal/cli/upgrade.go`, `ui/` (badge, alert).
- `internal/controller/talosoperation_controller.go`: the two refusal reasons set `status.failureReason` like `StepFailed`, which the CLI, the UI, the retry request (ADR 0095) and the "one unfinished operation" rule key on; a pause with only a condition reason would be resumed by the controller by itself. `controllerPolicyApp` no longer treats a policy the controller wrote itself as Ichor's installed policy.
- `ichor version` shows the installer image line; `PlanOperation` warns on a skip; a new Ichor upgrade replaces the installer settings (a pin or skip is written even when the version is already the target, so "retry with a skip" after a refusal works: delete the paused operation, run the upgrade with the flag).
- Docs: `docs/guides/install-with-helm.md` ("The installer image on an upgrade"), `test/harness/ui-demo/README.md` (the demo upgrade passes `--installer-digest` and `--skip-signature-check`: the controller cannot resolve a demo tag, which only the nodes' containerd mirror serves), ADR 0108's follow-up table.

## Tests

Unit: the identity matches only its tag; a signature verified in a virtual Sigstore passes only for the right identity, issuer and digest; a certificate from another CA fails; a registry with no signature, an unparsable bundle and an attestation referrer are refused; `ResolveDigest` against an in-memory registry. Controller (envtest): the Job carries `repo@digest`, a pinned digest skips resolution, a failed resolution and a failed verification pause with their reasons and create no Job, `skip` records `Skipped`, the reason and a Warning Event, a changed pin pauses. API server (envtest): the Job-image policy accepts the digest form and refuses a tag and a different digest, the CRD refuses `skip` without a reason and a malformed digest. UI backend: the plan warns on skip, create writes and clears the fields. CLI: the flags and the warning. UI: the badge appears for skip.

## Hardware verification (later)

See the report that accompanies this ADR; the positive path with a real signature needs a release tag built by `release.yml`.
