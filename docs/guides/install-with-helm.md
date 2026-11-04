# Install Ichor with Helm

This guide installs Ichor itself with `helm install` and then registers an
existing Talos cluster with `ichor adopt`. The design and the reasons are in
[ADR 0104](../adr/0104-a-cluster-with-no-cni-gets-ichors-cilium-before-ichor-and-ichor-is-installable-with-helm.md).

Status: ADR 0104 is built and hardware-verified (cases E1 to E7 on Talos v1.14
KubeVirt VMs, 2026-10-30; see the ADR's "Hardware results"). Not yet run on bare metal.

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
| `networkPolicy.cilium.enabled` | `null` | Ichor's `CiliumNetworkPolicy` objects (see [Network policies](#network-policies)). `null` renders them when the cluster serves Cilium's `CiliumNetworkPolicy` API, `true` renders them regardless (Cilium's CRD must exist), `false` renders none. |
| `networkPolicy.cilium.metricsNamespaces`, `metricsPodLabels` | `[]`, `{}` | Who may open the controller's metrics port. Empty: nobody. Name your Prometheus's namespace, and optionally its pod labels. |
| `preflight.cni` | `true` | Refuse to install when every node is NotReady with the network-plugin message. Skipped by `helm template` and `--dry-run`. |
| `installer.enabled` | `false` | Leave off: a Helm install is upgraded by Helm. |
| `ichorUI.port` | `8443` | The HTTPS port of ichor-ui. |
| `ichorUI.tls.secretName` | unset | Your own certificate for ichor-ui. Unset uses a leaf from the Ichor serving CA. |
| `ichorUI.tls.caConfigMap`, `caSecret`, `caKey` | `ichor-ui-ca`, unset, `ca.crt` | The CA the ServiceMonitor (`prometheus.enabled`) verifies the HTTPS port against: the ConfigMap, or the Secret when `caSecret` is set. |
| `settings.readOnly` | `false` | Hand the runtime settings to the chart: the `IchorInstallation` settings fields are ignored and the webhook denies writes. |
| `updateCheck.enabled` | `true` | Look for a newer Ichor release in the chart repository and show it on the dashboard (see [Settings](settings.md#updates)). `false` stops the requests and removes their egress rule. |

Adopt does not apply the network policies on a Helm install. When they apply,
it tells you to run:

```
helm upgrade ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z \
  --namespace ichor-system --reuse-values --set networkPolicy.cilium.enabled=true
```

## Network policies

On a cluster whose CNI is Cilium, Ichor's namespace is default-deny in both
directions with one allow per flow (ADR 0100, ADR 0126). The chart renders them
(`networkPolicy.cilium.enabled`), and `ichor init` and the installer Job apply
the same set on any other install. Nothing outside the cluster is allowed by a
fixed rule. What Ichor reaches is named by the controller, which keeps three
policies of its own from the objects that name them:

| Policy | Allows | Taken from |
|---|---|---|
| `ichor-controller-talos-egress` | the controller and ichor-ui to Talos nodes and cluster endpoints, and the controller to S3 stores | the endpoints and addresses of `TalosCluster`, `TalosMachine`, `MachineReclaim` and `TalosEtcdBackup` |
| `ichor-controller-release-check` | the controller to the chart and image registries of the release check | the repositories the controller is configured with; absent while `updateCheck` is off |
| `ichor-ui-oidc-egress` | ichor-ui to its external OIDC issuer | `ICHOR_OIDC_ISSUER` or `--oidc-issuer` of the ichor-ui Deployment; absent without one |

Names are matched with Cilium's `toFQDNs`, which needs the DNS proxy: Cilium's
`enable-l7-proxy` must be on (it is in the Cilium Ichor installs) and the pods'
DNS goes through the rule to `kube-dns` that Ichor's policies carry. On a Cilium
you manage yourself with the L7 proxy off, a name is never allowed: set
`networkPolicy.cilium.dynamicTalosEgress=false` to fall back to `world` rules, or
turn Ichor's policies off with `networkPolicy.cilium.enabled=false`.
`externalCIDRs` and `talosCIDRs` add fixed addresses next to the named ones.

A new issuer on the ichor-ui Deployment reaches ichor-ui within about a minute.

**Metrics.** The controller's metrics port (8443) is closed to every pod until
you name a scraper. With the chart, set
`networkPolicy.cilium.metricsNamespaces` (and `metricsPodLabels`):

```
helm upgrade ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z \
  --namespace ichor-system --reuse-values \
  --set 'networkPolicy.cilium.metricsNamespaces={monitoring}' \
  --set 'networkPolicy.cilium.metricsPodLabels.app\.kubernetes\.io/name=prometheus'
```

With `ichor init` or the installer Job there is no value to carry the
namespace; add a policy of your own, which adds to Ichor's (change the
namespace and labels to your Prometheus's, and the port if you changed it):

```
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: allow-metrics-scrape
  namespace: ichor-system
spec:
  endpointSelector:
    matchLabels:
      control-plane: controller-manager
      app.kubernetes.io/name: ichor
  ingress:
  - fromEndpoints:
    - matchLabels:
        k8s:io.kubernetes.pod.namespace: monitoring
        k8s:app.kubernetes.io/name: prometheus
    toPorts:
    - ports:
      - port: "8443"
        protocol: TCP
```

The scraper also needs the `metrics-reader` ClusterRole. ichor-ui's `/metrics`
shares the port the UI is served on, so no network policy can separate it; it
answers only a caller whose token may `get` `/metrics`.

## Verify a release

Every release image (`ichor-controller`, `ichor-ui`, `ichor-installer`) and the
chart is signed keylessly with cosign by the release workflow, and carries an
SPDX SBOM and a build provenance attestation (ADR 0108). Verify by digest, with
the identity of this repository's workflow:

```
cosign verify ghcr.io/maeshinshin/ichor-controller:vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/maeshinshin/ichor/\.github/workflows/release\.yml@refs/tags/v.*$'
```

The same command verifies `ichor-ui`, `ichor-installer` and the chart
(`ghcr.io/maeshinshin/charts/ichor:X.Y.Z`). The provenance attestation is
checked with `gh attestation verify oci://ghcr.io/maeshinshin/ichor-controller:vX.Y.Z --repo maeshinshin/ichor`.
An Ichor upgrade made by the installer Job (`ichor upgrade ichor`) checks the
installer image's signature itself; see
[The installer image on an upgrade](#the-installer-image-on-an-upgrade). A Helm
install is upgraded with `helm upgrade`, so there verify the chart and images
yourself as above, or enforce signatures with an admission policy of your own.
The base image of each release is pinned by digest and named in the image's
`org.opencontainers.image.base.digest` annotation. The images are reproducible
from the tag: the same commit and the same Nix shell build the same binaries
(`-trimpath`, stripped, no build timestamps).

## The installer image on an upgrade

`ichor upgrade ichor VERSION` makes the controller run a one-shot installer Job
whose ServiceAccount holds nearly cluster-admin rights, so the image it runs is
checked first (ADR 0122). Before it creates the Job the controller:

1. resolves `<installerRepository>:VERSION` to a digest (or takes
   `spec.installerDigest`, a pin you set with `--installer-digest sha256:...`),
2. verifies that digest's keyless cosign signature, offline against a Sigstore
   trusted root built into the controller, for the identity
   `https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/VERSION`
   issued by GitHub Actions (the identity of the `cosign verify` command above),
3. runs the Job with the image `<installerRepository>@sha256:...`, so what was
   verified is what runs. The API server refuses an installer Job that names a tag.

If the tag does not resolve or the signature does not verify, the operation is
paused with the reason `InstallerImageUnresolved` or `InstallerImageUnverified`,
no Job is created and nothing is applied. `ichor operation get` shows why.
After you fix the registry or the mirror, `ichor operation resume NAME` runs the check
again. The digest and the result are in `ichor version` and in
`IchorInstallation` `status.installerImage`.

**Mirrors and air gaps.** The signature belongs to the digest, not to a
registry, so a mirror verifies as long as it holds the image under the same
digest and the image's OCI referrers (copy with a tool that copies referrers,
for example `oras copy --recursive`; `crane copy` and `docker push` do not).
The controller reads `installerRepository` anonymously, over an egress rule it
maintains for that host (for `ghcr.io` also `pkg-containers.githubusercontent.com`,
where GHCR redirects blobs). No other service is contacted. A registry that
needs credentials or redirects blobs to a host that is not allowed has no way
through except the pin plus the recorded skip below.

**Builds nobody signs.** Development and demo images (`v0.0.N-gSHA`) carry no
signature. Skip the check with a reason, which Ichor records:

```
ichor upgrade ichor v0.0.1900000001-g1a2b3c4 --skip-signature-check "demo build, not signed"
```

The plan prints a warning before you confirm, the controller emits a Warning
Event `InstallerSignatureSkipped`, `status.installerImage.verification` becomes
`Skipped` with the reason, `ichor version` says "signature NOT checked", and the
UI shows an "Installer not verified" badge on every page and an alert on the
cluster page. The skip is never inherited: the next `ichor upgrade ichor`
without the flag checks again (the CLI and the UI write the setting exactly as
given). There is no controller flag or chart value that turns the check off.
A fork that signs its own images has no way to name its own signer yet; it
skips the check the same way.

This protects the upgrade after the one that installs it: the first upgrade onto
a release with this check is made by the previous controller, which creates the
Job by tag. The controller and UI images that the installer then applies are
still tags, not digests (ADR 0122, residual risk).

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
- Network: see [Network policies](#network-policies): default-deny plus one
  allow per flow, with no `world` egress left.
- Releases are published only after the verify job passes, from a protected
  `release` GitHub environment. Maintainers: create the environment with
  required reviewers and a tag ruleset for `v*` in the repository settings.

## ichor-ui is HTTPS only

ichor-ui serves TLS on 8443 and no plain HTTP (ADR 0105). The Service port is
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
