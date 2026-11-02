# Install Ichor with Helm

This guide installs Ichor itself with `helm install` and then registers an
existing Talos cluster with `ichor adopt`.

Status: built. Its hardware tests have not been run, so treat this
path as untested on real clusters until they have.

The chart installs Ichor, not a cluster: CRDs (kept on uninstall), the
controller and its RBAC, the webhooks, the UI and the optional Cilium policies.
Registering a cluster needs your talosconfig and secrets bundle, so that stays
with `ichor adopt`. The chart does not ship Cilium.

## Where the chart lives

The chart is published as an OCI artifact, with the images next to it, by a
workflow that runs when a `v*` tag is pushed:

```
oci://ghcr.io/maeshinshin/charts/ichor
```

No release has been tagged yet, so that address has nothing in it today. Until
the first release, install from a checkout:

```
helm install ichor ./dist/chart --namespace ichor-system --create-namespace \
  --set manager.image.repository=REPO --set ichorUI.image.repository=REPO
```

The default image repositories are `ghcr.io/maeshinshin/ichor-controller` and
`ghcr.io/maeshinshin/ichor-ui`, which will not exist until the first release
either, so set your own. The chart `version` has no leading `v` (for example
`0.1.0`); `appVersion` and the image tags have it (`v0.1.0`). The published
images are linux/amd64 only. Helm is not provided by Ichor; install it
yourself.

## Install

For a cluster that already has a pod network:

```
helm install ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z \
  --namespace ichor-system --create-namespace
ichor adopt --cluster NAME --node ADDR --recover-secrets --recover-from ADDR
```

Values worth knowing (see `dist/chart/values.yaml` for all of them):

| Value | Default | Meaning |
|---|---|---|
| `networkPolicy.cilium.enabled` | `null` | Ichor's `CiliumNetworkPolicy` objects. Needs Cilium's CRD, so turn it on only once Cilium runs. `null` renders none (nothing else applies them on a Helm install), `true` renders them, `false` says so explicitly. |
| `preflight.cni` | `true` | Refuse to install when every node is NotReady with the network-plugin message. Skipped by `helm template` and `--dry-run`. |
| `installer.enabled` | `false` | Leave off: a Helm install is upgraded by Helm. |
| `ichorUI.port` | `8443` | The HTTPS port of ichor-ui. |
| `ichorUI.tls.secretName` | unset | Your own certificate for ichor-ui. Unset uses a leaf from the Ichor serving CA. |
| `ichorUI.tls.caConfigMap`, `caSecret`, `caKey` | `ichor-ui-ca`, unset, `ca.crt` | The CA the ServiceMonitor (`prometheus.enabled`) verifies the HTTPS port against: the ConfigMap, or the Secret when `caSecret` is set. |
| `settings.readOnly` | `false` | Hand the runtime settings to the chart: the `IchorInstallation` settings fields are ignored and the webhook denies writes. |

Adopt does not apply the network policies on a Helm install. When they apply,
it tells you to run:

```
helm upgrade ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z \
  --namespace ichor-system --reuse-values --set networkPolicy.cilium.enabled=true
```

## Verify a release

Every release image (`ichor-controller`, `ichor-ui`, `ichor-installer`) and the
chart is signed keylessly with cosign by the release workflow, and carries an
SPDX SBOM and a build provenance attestation. Verify by digest, with
the identity of this repository's workflow:

```
cosign verify ghcr.io/maeshinshin/ichor-controller:vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/maeshinshin/ichor/\.github/workflows/release\.yml@refs/tags/v.*$'
```

The same command verifies `ichor-ui`, `ichor-installer` and the chart
(`ghcr.io/maeshinshin/charts/ichor:X.Y.Z`). The provenance attestation is
checked with `gh attestation verify oci://ghcr.io/maeshinshin/ichor-controller:vX.Y.Z --repo maeshinshin/ichor`.
Ichor does not check signatures itself before it runs the installer Job (the
Job's image is `installerRepository:version`, resolved by tag; resolving it to a
digest is not built). Verify the image yourself, or enforce signatures with an
admission policy of your own. The base image of each release is pinned by
digest and named in the image's `org.opencontainers.image.base.digest`
annotation. The images are reproducible from the tag: the same commit and the
same Nix shell build the same binaries (`-trimpath`, stripped, no build
timestamps).

## Hardening the install

- Pod Security: the install manifest labels `ichor-system` with
  `pod-security.kubernetes.io/enforce: restricted` (and audit and warn). Every
  Ichor pod meets that profile; the Cilium Helm Job needs the host network and
  runs in `kube-system`. The chart does not own the namespace, so a Helm install
  labels it once: `kubectl label namespace ichor-system
  pod-security.kubernetes.io/enforce=restricted
  pod-security.kubernetes.io/audit=restricted
  pod-security.kubernetes.io/warn=restricted`.
- Priority: both pods run under the PriorityClass `ichor-critical` (value
  1000000), created by the manifest and by the chart (`priorityClass.create`).
  It ranks above ordinary workloads and below every `system-*` class, so the
  controller, the only writer to Talos nodes, is evicted last of the user
  workloads. `system-cluster-critical` is not used: a user namespace may not
  use it by default.
- Replicas: the raw install manifest runs one replica of each pod and has no
  PodDisruptionBudget (a budget of `minAvailable: 1` on one replica would block
  every node drain). The chart runs two replicas and ships the budgets.
- Network: `networkPolicy.cilium.enabled: true` renders default-deny plus one
  allow per flow. Egress to the outside is `world` by default because Talos
  nodes that have not joined yet are outside the cluster. To narrow it, set
  `networkPolicy.cilium.externalCIDRs` (the S3 endpoint and the OIDC issuer, on
  `externalPorts`), `talosCIDRs` (apid on 50000 and the API server on 6443; include
  the subnet of machines that have not joined) and `uiIngressCIDRs` (who may
  reach ichor-ui from outside the cluster; in-cluster callers stay allowed). These
  values apply to the chart's policies; the copy the installer applies keeps
  `world`.
- Releases are published only after the verify job passes, from a protected
  `release` GitHub environment. Maintainers: create the environment with
  required reviewers and a tag ruleset for `v*` in the repository settings.

## ichor-ui is HTTPS only

ichor-ui serves TLS on 8443 and no plain HTTP. The Service port is
named `https`. The certificate is yours (`ichorUI.tls.secretName`) or a leaf from
the Ichor serving CA, whose public part the controller publishes as the
ConfigMap and Secret `ichor-ui-ca` in `ichor-system`. How to put an Ingress, a
Gateway or a load balancer in front is in [Users and login](users-and-login.md#expose-ichor-ui-over-https).

Moving an existing release:

- Plain HTTP edge: ichor-ui no longer serves plain HTTP. Re-point the edge at
  `https://<ichor-ui>:8443` and trust `ichor-ui-ca` before you upgrade. The
  value `ichorUI.plaintextPort` was removed; a values file that still sets it
  is ignored.
- An install that already served TLS on 8080 sets `ichorUI.port=8080` to keep
  its Service and edge as they are.
- The ServiceMonitor verifies against `ichor-ui-ca` by default; override with
  `ichorUI.tls.caConfigMap`, `caSecret` and `caKey`. It never skips
  verification.

## Settings on a Helm install

See [Settings](settings.md#a-helm-install). `settings.readOnly: true` makes the
chart the only source.

## A cluster with no CNI

Its nodes are NotReady, so the chart's pods could not start and `helm install
--wait` would hang. The chart's preflight refuses to install and says so. The
sequence is:

```
ichor cni bootstrap --kubeconfig FILE --kube-proxy enabled|replaced [--tunnel vxlan|geneve] [--cilium-values FILE]
helm install ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z \
  --namespace ichor-system --create-namespace
ichor adopt --cluster NAME --node ADDR --recover-secrets --recover-from ADDR
```

- `ichor cni bootstrap` creates `ichor-system` if needed, installs the pinned
  Cilium as Ichor's own Helm release (release `cilium`, records in
  `ichor-system`), and waits for the nodes to be Ready. It asks for
  confirmation unless `--yes`. Running it again is safe.
- `--kube-proxy` is required: `enabled` if Talos runs kube-proxy, `replaced` if
  it is disabled in Talos. The command has no Talos credentials, so it cannot
  read this, and it cannot check KubePrism either. KubePrism must be enabled on
  port 7445 on every node (Talos's default); adopt checks it later.
- `ichor adopt` then finds Cilium with an Ichor release record, and checks the
  tunnel and kube-proxy mode against it.

What adopt does on a Helm install is in
[Adopt a running cluster](adopt-and-reclaim.md#when-ichor-was-installed-with-helm).

## Upgrade and uninstall

Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade
with it. `ichor` commands that would run the installer Job refuse on a Helm
install and print the Helm command. The UI server's detection of a Helm install
is limited, so some of its upgrade prompts may still describe the installer
path. `helm uninstall` keeps the CRDs and therefore every cluster object, and
does not touch Cilium, which is a separate release.
