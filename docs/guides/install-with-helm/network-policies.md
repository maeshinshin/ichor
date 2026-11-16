---
sidebar_order: 3
---

# Network policies

On a cluster whose CNI is Cilium, Ichor's namespace is default-deny in both
directions with one allow per flow. The chart renders them
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

## Metrics

The controller's metrics port (8443) is closed to every pod until
you name a scraper. With the chart, set
`networkPolicy.cilium.metricsNamespaces` (and `metricsPodLabels`):

```sh
helm upgrade ichor ichor/ichor --version X.Y.Z \
  --namespace ichor-system --reuse-values \
  --set 'networkPolicy.cilium.metricsNamespaces={monitoring}' \
  --set 'networkPolicy.cilium.metricsPodLabels.app\.kubernetes\.io/name=prometheus'
```

The scraper also needs the `metrics-reader` ClusterRole. ichor-ui's `/metrics`
shares the port the UI is served on, so no network policy can separate it; it
answers only a caller whose token may `get` `/metrics`.

### Without a chart value

With `ichor init` or the installer Job there is no value to carry the
namespace; add a policy of your own, which adds to Ichor's (change the
namespace and labels to your Prometheus's, and the port if you changed it):

```yaml
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
