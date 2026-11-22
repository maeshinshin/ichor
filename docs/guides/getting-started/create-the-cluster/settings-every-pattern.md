---
sidebar_order: 3
---

# Settings every pattern shares

The settings every endpoint pattern shares.

## The shape of the cluster

A fresh install runs Talos v1.14.5 and Kubernetes v1.36.6 under contract `v1.14`;
init refuses other versions.

| Setting | Meaning |
|---|---|
| `--preset` | `standard` (default): three control planes, workers added later and tainted. `single`: one schedulable node. `compact-ha`: three schedulable control planes. `large`: five control planes, workers later |
| `--cni-tunnel geneve` | for virtual networks that pass geneve but not vxlan (Cilium's default) |
| `--gateway-api`, `--l2-announcements` with `--l2-pool` | Cilium Gateway API and LAN LoadBalancer addresses; need `--kube-proxy replaced`. See [LAN load balancers](../../lan-load-balancers.md) |
| `--pod-subnets`, `--service-subnets` | default `10.244.0.0/16` and `10.96.0.0/12`; must not overlap the nodes' network and are fixed once the cluster exists |
| `--registry-mirror HOST=URL` | anonymous mirror in `spec.registries`, repeatable |
| `--install-disk` | `wwid:ID`, `serial:SERIAL`, a `/dev/disk/by-id/` path or a device such as `/dev/sda` |

`ichor init --help` lists the rest. Talos puts the endpoint in the certificate it
issues on each control plane, so `kubectl` verifies against it with every
pattern, a load balancer's DNS name included.

## The init.yaml file

`ichor init -f init.yaml` reads the same settings; `--print-config-template`
prints a commented example. A flag overrides the file. Paths in the file are
relative to it, and it holds a key's path, never the key.

## The secrets bundle

Init first writes the encrypted secrets bundle, the cluster's root of trust, to
`~/.ichor/clusters/demo/secrets.yaml` (mode 0600). On a terminal it generates a
passphrase, prints it once and waits for Enter. The file is an age file.

> [!IMPORTANT]
> Save the passphrase in a password manager, together with a copy of
> `secrets.yaml` kept somewhere off this machine. Without the passphrase the
> file cannot be opened.

To choose the passphrase, or with no terminal, use `--bundle-passphrase-file
./pass` (may be `/dev/fd/3`) or `ICHOR_BUNDLE_PASSPHRASE` (12 characters or more);
with neither init refuses before doing anything.

> [!WARNING]
> `--bundle-plaintext` stores the file unencrypted. Use it only for a throwaway
> cluster.

See [Users and login](../../users-and-login.md) and [Hardening](../../hardening.md).
Init then does the rest, in order; expect several minutes.

![The steps of ichor init after the bundle is written, in order: apply the configuration to the node, which installs Talos and reboots; bootstrap etcd; save the kubeconfig next to the bundle; store the bundle in the cluster; install Ichor; and last, Ichor takes over Cilium.](../../../images/init-sequence.light.drawio.svg)

*What init does, in order.*

## Your kubeconfig

`--merge-kubeconfig` merges the cluster-admin kubeconfig into yours (first file
of `$KUBECONFIG`, else `~/.kube/config`) as context `admin@demo` and makes it
current; a taken name gets a suffix and init prints the final one. Without it,
init prints the `export KUBECONFIG=` line the later steps use.

## Sign-in

Sign-in is on and cannot be turned off. Init creates the first admin (`admin`, or
`--admin-user`) and prints its password to stderr once; the first login forces a
new one. Without `--ui-url` the issuer is `https://localhost:8443`, served by
`ichor ui`. To publish ichor-ui yourself, give the https URL browsers will use and
a certificate:

```sh
  --ui-url https://192.168.1.60 --admin-user admin \
  --ui-tls-cert ui.crt --ui-tls-key ui.key
```

In the file this is a `ui` block, next to `cluster` and `firstNode`:

```yaml title="init.yaml"
ui:
  url: https://192.168.1.60
  adminUser: admin
  tls:
    certFile: ./ui.crt
    keyFile: ./ui.key
```

The URL is the issuer of every login. Making it reachable (a LoadBalancer,
Ingress or Gateway on ichor-ui's port 8443) is yours; init installs none. The
kube-apiserver then needs the new login settings:

```sh
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
ichor rollout config --cluster demo --follow
```

With one control plane the Kubernetes API is down for about a minute.
