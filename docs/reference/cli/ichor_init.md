---
title: "ichor init"
description: "Bootstrap a brand-new Talos cluster's first control plane"
---

## ichor init

Bootstrap a brand-new Talos cluster's first control plane

### Synopsis

init brings a single, freshly-booted Talos node (in maintenance mode, not
yet configured) under Ichor's management as the first control plane of a
new cluster:

  1. Generates a secrets bundle locally and backs it up to
     ~/.ichor/clusters/<cluster>/secrets.yaml (0600), encrypted with a
     passphrase (--bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or one
     generated and shown once on a terminal) -- this is the
     cluster's only root of trust. BACK IT UP, and the passphrase, before
     doing anything else.
  2. Settles the cluster's shape from flags (topology preset, CNI, endpoint).
  3. Renders and applies that configuration to --node-endpoint using
     Talos's insecure apply. THIS STEP IS IRREVERSIBLE: insecure apply does
     not authenticate the node, so a separate provisioning network is
     assumed; the node will install Talos and reboot.
  4. Bootstraps etcd, fetches the kubeconfig (saved alongside the secrets
     bundle), and stores the bundle as a Secret in the new cluster.
  5. Creates the TalosCluster, TalosMachine and any TalosConfigPatch
     objects the CNI needs, and the IchorInstallation.
  6. Waits for the controller to hand Cilium over to Ichor's Helm release
     (TalosCluster CNIHandover).

With --endpoint-type vip the endpoint address is a shared IP that Ichor puts on
every control plane, on its primary link: init takes that link from the node
(the only physical link, or one you pick; --nic names it by link name or permanent MAC),
gives the node DHCP on that link only, and the configuration refers to it as
net0. A control plane approved later needs its own `--nic` for the same reason.
The address must be a free IP, not the node's own.

init never applies anything to a node again after step 3; every later
change to this cluster goes through the controller, by writing CRs.

--dry-run reads the first node and prints what init would do, with the disk and link it would use,
and writes nothing and sends nothing to a node.

The settings can come from a file instead of flags: -f init.yaml (an
InitConfig; --print-config-template prints an example). A flag on the command
line overrides the file; relative paths in the file are relative to it.

On a terminal, a required setting given by neither is asked for; with no
terminal init fails as before, naming the missing flag.

```sh
ichor init [flags]
```

### Examples

```sh
  ichor init --print-config-template > init.yaml
  ichor init -f init.yaml
```

### Options

```text
      --address strings                    static address of the first node's primary link with prefix length, 192.168.1.10/24 (repeatable); without it the node uses DHCP on that link. Same as approve node --address
      --admin-user string                  with --ui-url: name of the first admin (default admin); its generated password is printed once to stderr and must be changed at the first sign-in
      --bundle-plaintext                   store the secrets bundle unencrypted (default: encrypted with --bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or a passphrase generated on a terminal)
      --cert-san stringArray               extra name or IP address that the Talos API and kube-apiserver certificates both carry (spec.certSANs); repeatable. Each node's own addresses are already in its certificates, and the endpoint is in the kube-apiserver one. Changing the list after init reissues the certificates and reboots every node, so set it now
      --cluster string                     name of the TalosCluster to create (required)
      --cni-tunnel string                  Cilium's tunnel protocol, vxlan or geneve (default: Cilium's vxlan); geneve for virtualised networks that pass geneve but not vxlan
      --discover-address stringArray       IPv4 address of a node booted into maintenance mode for the controller to probe (spec.discovery.addresses); repeatable
      --discover-cidr stringArray          IPv4 CIDR (/24 or smaller) of maintenance-mode nodes for the controller to probe (spec.discovery.cidrs); repeatable
      --dns strings                        DNS server address for the first node (repeatable)
      --dry-run                            read the first node and print what init would do, without writing anything or sending anything to a node
      --endpoint-address string            cluster endpoint address (required): an IP address that no node has yet for vip, which Ichor puts on the control planes
      --endpoint-type string               cluster endpoint type: vip, loadBalancer or node (default "vip")
      --ephemeral-disk-selector string     CEL disk selector (e.g. disk.wwid == "...") for a dedicated EPHEMERAL disk, separate from --install-disk
  -f, --file string                        read the init settings from this InitConfig file (see --print-config-template); a flag given on the command line overrides the file
      --gateway string                     with --address: default gateway. Left out, the configuration adds no default route; a route the node already has from its platform configuration (Talos merges the layers) is kept
      --gateway-api                        turn on Cilium's Gateway API and put its CRDs in the bootstrap; init only, an existing cluster is not changed by it; needs kube-proxy replacement, which init does not build yet, so it is refused for now
  -h, --help                               help for init
      --install-disk string                the first node's install disk: "wwid:ID", "serial:SERIAL", a /dev/disk/by-id/ path, a bus path such as /pci0000:00/..., or a device path such as /dev/sda (required)
      --installer-digest string            pin Ichor's installer image to this digest (sha256:...) in the IchorInstallation instead of letting the controller resolve the version's tag; for a cluster whose controller cannot reach the registry (a demo behind a mirror)
      --kube-api-server-url string         with --ui-url: https URL of the Kubernetes API put in users' kubeconfigs (chart: ichorUI.users.kubeAPIServerURL)
      --kube-api-tls-server-name string    with --ui-url: tls-server-name in users' kubeconfigs (chart: ichorUI.users.kubeAPITLSServerName)
      --kube-proxy string                  kube-proxy mode: enabled (Talos's own, the default) or replaced (Cilium replaces it and reaches the API through KubePrism on port 7445, which Talos enables by default); --gateway-api and --l2-announcements need replaced; init only, an existing cluster is not changed by it (default "enabled")
      --l2-announcements                   turn on Cilium's L2 announcements, which answer ARP for LoadBalancer addresses on the LAN; init only, an existing cluster is not changed by it; needs --l2-pool and kube-proxy replacement, which init does not build yet, so it is refused for now
      --l2-pool string                     with --l2-announcements: the IPv4 CIDR that LoadBalancer addresses come from; init checks it against the node and the endpoint and does not write the CiliumLoadBalancerIPPool, which you create yourself
      --merge-kubeconfig                   also merge the cluster-admin kubeconfig into your kubeconfig (--kubeconfig, else the first file of $KUBECONFIG, else ~/.kube/config) as context admin@CLUSTER, and make it current; a taken name gets a suffix
  -n, --namespace string                   namespace the TalosCluster, its TalosMachine and the secrets bundle Secret live in; Ichor's own install manifest always deploys its controller to "ichor-system" (default "ichor-system")
      --nic string                         the first node's primary network link, by its name (enp1s0) or permanent MAC (aa:bb:cc:dd:ee:ff), the link its addressing goes on (a vip is announced on it too); like approve node --nic. A name is looked up among the links the node reports and only its MAC is kept. Needed only when the node has more than one physical link
      --node-endpoint string               Talos API address (host:port) of the first node, currently in maintenance mode (required)
      --pod-subnets strings                pod CIDRs (spec.network.podSubnets; at most one per IP family); unset keeps Talos's default 10.244.0.0/16. Fixed once the cluster exists
      --preset string                      topology preset: single (1 node, schedulable), compact-ha (3 control planes, schedulable), standard (3 control planes + workers added later, tainted; recommended), or large (5 control planes + workers added later, tainted) (default "standard")
      --print-config-template              print an example InitConfig file and exit
      --registry-mirror stringArray        anonymous registry mirror as HOST=URL (spec.registries), e.g. ghcr.io=http://10.0.0.5:5000; repeatable
      --service-subnets strings            service CIDRs (spec.network.serviceSubnets); unset keeps Talos's default 10.96.0.0/12. Fixed once the cluster exists
      --skip-signature-check               run the installer image WITHOUT checking its signature (for builds nobody signs: development and demo images)
      --storage-disk-selector string       CEL disk selector for a disk prepared for a storage system (RawVolumeConfig)
      --storage-volume-name string         name for --storage-disk-selector's volume (default "storage")
      --ui-tls-cert string                 with --ui-url: PEM certificate file for ichor-ui's TLS; init stores it with --ui-tls-key as a Secret
      --ui-tls-key string                  with --ui-tls-cert: PEM private key file (never printed)
      --ui-tls-secret string               with --ui-url: serve ichor-ui over TLS with this existing kubernetes.io/tls Secret in ichor-system (chart: ichorUI.tls.secretName)
      --ui-url string                      turn Ichor's own users on: the https URL at which browsers and the CLI reach ichor-ui, the issuer of every token; needs --version-contract v1.14 or later
      --user-volume-disk-selector string   CEL disk selector for a UserVolumeConfig disk
      --user-volume-name string            name for --user-volume-disk-selector's volume (default "data")
      --vlan int32                         VLAN ID (1-4094) to put the first node's addressing on
      --wait-timeout duration              how long to wait for Ichor's controller to serve its admission webhooks before writing the first resources (default 30m0s)
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

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs

