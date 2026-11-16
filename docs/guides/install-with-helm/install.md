---
sidebar_order: 2
---

# Install the chart

## Where the chart lives

The chart is published by a workflow that runs when a `v*` tag is pushed, as a
Helm repository on the docs site and as an OCI artifact, with the images next
to it. Both hold the same signed archive:

```sh
helm repo add ichor https://ichor.maesh.dev/charts
helm repo update
```

```text
oci://ghcr.io/maeshinshin/charts/ichor
```

The chart's default images are `ghcr.io/maeshinshin/ichor-controller` and
`ghcr.io/maeshinshin/ichor-ui`, by digest. To install a chart and images you
built yourself, see [Build from source](../../development/build-from-source.md#install-your-own-chart).
The chart `version` has no leading `v` (for example
`{{version}}`); `appVersion` and the image tags have it (`v{{version}}`). The published
images are multi-arch (linux/amd64 and linux/arm64). Helm is not provided by Ichor; [install it](https://helm.sh/docs/intro/install/)
yourself.

## Install

For a cluster that already has a pod network:

```sh
helm install ichor ichor/ichor --version X.Y.Z \
  --namespace ichor-system --create-namespace \
  --set ichorUI.users.enabled=true --set ichorUI.users.externalURL=https://localhost:8443
ichor create user admin --role admin --generate --bootstrap
ichor adopt --cluster NAME --node ADDR --recover-secrets --recover-from ADDR
```

The OCI form installs the same chart without adding the repository:
`helm install ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z …`.

Adopt does not apply the network policies on a Helm install. When they apply,
it tells you to run:

```sh
helm upgrade ichor ichor/ichor --version X.Y.Z \
  --namespace ichor-system --reuse-values --set networkPolicy.cilium.enabled=true
```

## When the install stops on the retired-kinds guard

Before an install or upgrade, the chart runs a hook Job, `ichor-retired-kinds-guard`,
that refuses while an object of the retired kind `TalosStorage` still exists. When
the Job never gets to run, Helm prints a line that ends in `Job in progress` and
`context deadline exceeded`. That line does not say which of two things happened:

- **The guard refused.** Its pod ran and exited 1. Delete the objects it lists, then install again.
- **The guard never ran.** Its pod was not scheduled, could not pull its image, or was killed. Deleting objects does not help; fix the cluster or the node, then install again.

Run this to tell them apart:

```sh
kubectl -n ichor-system describe pods -l batch.kubernetes.io/job-name=ichor-retired-kinds-guard
```

- `State: Terminated` with `Exit Code: 1` ran. `kubectl -n ichor-system logs job/ichor-retired-kinds-guard` lists the retired objects, or an API error when the guard could not read them (fix its RBAC or the cluster in that case).
- `Pending`, or `Waiting` with `ErrImagePull` or `ImagePullBackOff`, or a `FailedScheduling` event, never ran.
- Any other exit code or reason, such as `OOMKilled` or `Evicted`, means the pod was killed, not refused.
- No pods at all: `kubectl -n ichor-system describe job ichor-retired-kinds-guard` shows why the pod was never created.

The chart keeps a failed hook's Job and pod, because its delete policy removes a
hook only when it succeeds. A new attempt deletes them before it starts.

## Values

Values worth knowing (see `dist/chart/values.yaml` for all of them):

| Value | Default | Meaning |
|---|---|---|
| `networkPolicy.cilium.enabled` | `null` | Ichor's `CiliumNetworkPolicy` objects (see [Network policies](network-policies.md)). `null` renders them when the cluster serves Cilium's `CiliumNetworkPolicy` API, `true` renders them regardless (Cilium's CRD must exist), `false` renders none. |
| `networkPolicy.cilium.metricsNamespaces`, `metricsPodLabels` | `[]`, `{}` | Who may open the controller's metrics port. Empty: nobody. Name your Prometheus's namespace, and optionally its pod labels. |
| `preflight.cni` | `true` | Refuse to install when every node is NotReady with the network-plugin message. Skipped by `helm template` and `--dry-run`. |
| `installer.enabled` | `false` | Leave off: a Helm install is upgraded by Helm. |
| `ichorUI.port` | `8443` | The HTTPS port of ichor-ui. |
| `ichorUI.tls.secretName` | unset | Your own certificate for ichor-ui. Unset uses a leaf from the Ichor serving CA. |
| `ichorUI.tls.caConfigMap`, `caSecret`, `caKey` | `ichor-ui-ca`, unset, `ca.crt` | The CA the ServiceMonitor (`prometheus.enabled`) verifies the HTTPS port against: the ConfigMap, or the Secret when `caSecret` is set. |
| `settings.readOnly` | `false` | Hand the runtime settings to the chart: the `IchorInstallation` settings fields are ignored and the webhook denies writes. |
| `updateCheck.enabled` | `true` | Look for a newer Ichor release in the chart repository and show it on the dashboard (see [Updates](../settings/updates.md)). `false` stops the requests and removes their egress rule. |

## Settings on a Helm install

See [Settings](../settings.md#a-helm-install). `settings.readOnly: true` makes the
chart the only source.
