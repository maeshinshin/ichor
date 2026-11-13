---
title: "ichor bootstrap cni"
description: "Install Ichor's Cilium on a cluster with no CNI, before helm install ichor"
---

## ichor bootstrap cni

Install Ichor's Cilium on a cluster with no CNI, before helm install ichor

### Synopsis

bootstrap cni is the first step of the Helm path on a cluster with no CNI: such a
cluster cannot run any pod, so Ichor's chart cannot start. bootstrap installs Ichor's pinned Cilium
as Ichor's own Helm release (release cilium in kube-system, its record in ichor-system) with the
operator's kubeconfig, and waits for every node to be Ready. Then run helm install ichor, then
ichor adopt, which recognises this Cilium as Ichor's and does not take it over.

It refuses unless the cluster has no CNI: no CNI workload, no CiliumNode CRD, and every node
NotReady with the network-plugin message. It has no Talos credential, so it takes Talos's
cluster.network.cni.name to be none (the only way a cluster ends up like this) and reads nothing
else from Talos: --kube-proxy says whether Cilium replaces kube-proxy. Replaced: kube-proxy is
disabled in Talos, and Cilium reaches the API through KubePrism on port 7445. Enabled: Cilium's
API address is the chart's default. In both modes KubePrism must be enabled on port 7445 on every
node (adopt checks this again later). --tunnel (vxlan or geneve) is immutable afterwards.
--cilium-values is a YAML file of Cilium Helm values merged like spec.cni.cilium.values;
Ichor-owned keys are refused.

It shows what it will do and asks for confirmation unless --yes. Running it again is safe.

```sh
ichor bootstrap cni --kube-proxy enabled|replaced [flags]
```

### Examples

```sh
  ichor bootstrap cni --kube-proxy replaced
  ichor bootstrap cni --kube-proxy enabled --tunnel vxlan --yes
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cilium-values string     YAML file of Cilium Helm values (Ichor-owned keys are refused)
  -h, --help                     help for cni
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kube-proxy string        enabled (Talos runs kube-proxy) or replaced (Cilium replaces it) (required)
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --tunnel string            tunnel protocol, vxlan or geneve; immutable afterwards
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor bootstrap](ichor_bootstrap.md)	 - Install Ichor's Cilium on a cluster with no CNI, before helm install ichor

