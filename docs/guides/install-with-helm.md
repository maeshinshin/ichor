# Install Ichor with Helm

This guide installs Ichor itself with `helm install` and then registers an
existing Talos cluster with `ichor adopt`. The design and the reasons are in
[ADR 0104](../adr/0104-a-cluster-with-no-cni-gets-ichors-cilium-before-ichor-and-ichor-is-installable-with-helm.md).

Status: ADR 0104 is built. Its hardware tests have not been run, so treat this
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
  is accepted (Helm ignores unknown values) but renders no plaintext port, no
  `--http-addr` and no `http` Service port.
- An install that already served TLS on 8080 sets `ichorUI.port=8080` to keep
  its Service and edge as they are.
- The ServiceMonitor verifies against `ichor-ui-ca` by default; override with
  `ichorUI.tls.caConfigMap`, `caSecret` and `caKey`. It never skips
  verification.

## Settings on a Helm install

The controller creates the `IchorInstallation` `ichor` once with
`spec.deployment.manager: helm`; the chart does not render it. Reclaim, the
sign-in lifetimes and the default drift policy are editable at run time (see
[Settings](settings.md)) and win over the chart values when set. Set
`settings.readOnly: true` to make the chart the only source. Upgrading and
`ichor upgrade ichor` stay with Helm.

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

## What adopt does on a Helm install

- Skips its install step and creates no `IchorInstallation` (the controller seeds it).
- Requires Ichor's version to equal the CLI's exactly, and prints the `helm
  upgrade` command when it does not.
- `--require-ichor` makes adopt fail when Ichor is not installed instead of
  installing it.

## Upgrade and uninstall

Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade
with it. `ichor` commands that would run the installer Job refuse on a Helm
install and print the Helm command. The UI server's detection of a Helm install
is limited, so some of its upgrade prompts may still describe the installer
path. `helm uninstall` keeps the CRDs and therefore every cluster object, and
does not touch Cilium, which is a separate release.
