---
sidebar_order: 2
---

# 2. Create the cluster

`ichor init` makes the node the first control plane of a new cluster. It
prompts before the irreversible step; `--yes` skips that.

```sh
ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk`
  are required. `--node-endpoint` is the node's Talos API (`host:port`); the
  install disk can also be `wwid:ID`, `serial:SERIAL` or a
  `/dev/disk/by-id/` path.
- `--endpoint-type` is `vip` by default, which expects a free address on the
  nodes' network as `--endpoint-address`. `node` uses the node's own address,
  which is simplest for one control plane.
- `--preset` is `standard` by default (three control planes, workers added
  later). `single` is one schedulable node; `compact-ha` is three schedulable
  control planes.
- `--registry-mirror HOST=URL` writes an anonymous mirror into the machine
  config (`spec.registries`), for nodes that cannot reach a registry directly.
  It is repeatable and not needed here.
- On virtual networks that pass geneve but not vxlan, add
  `--cni-tunnel geneve` for Cilium.
- The pod and service subnets (`--pod-subnets` and `--service-subnets`, by
  default `10.244.0.0/16` and `10.96.0.0/12`) must not overlap the network the
  nodes sit on, and they cannot change once the cluster exists.
  `ichor init --help` lists the rest (disk selectors, discovery addresses).

## Answering at a prompt

On a terminal, the required settings can be left out and `ichor init` asks for
each one that was not given: the cluster name, the endpoint type, the
endpoint address and the node address. Settings given as flags are not asked
for again.

```console
$ ichor init --preset single
  Cluster name       demo
  Endpoint type      node (an IP address)
› Endpoint address   192.168.1.50▏
  an IP address is expected here

  enter confirm · esc cancel
```

The endpoint type is a list: typing narrows it, the arrow keys move, Enter
selects. Text fields check the answer as you type and say what is wrong. The
install disk is asked last, from the disks the node reports; a node with one
disk has it chosen and named. Esc or Ctrl-C cancels and nothing is written.

With no terminal (in CI, or from a pod) there is no prompt: a missing setting
fails with the same "required flag" error as always, so scripts are
unaffected. Use the flags there.

The same settings can come from a file instead of flags. `ichor init -f
init.yaml` reads an `InitConfig`; `ichor init --print-config-template`
prints a commented example. The command above is this file:

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: single
  endpoint:
    type: node
    address: 192.168.1.50
firstNode:
  endpoint: 192.168.1.50:50000
  installDisk: /dev/sda
```

A flag on the command line overrides the file. Paths in the file (the TLS
certificate and key below) are relative to the file. The file never holds the
key itself, only its path.

Init first writes the secrets bundle to `~/.ichor/clusters/demo/secrets.yaml`
(mode 0600), encrypted. It is the cluster's root of trust. On a terminal, init
generates a passphrase, prints it once and waits for Enter: save it in a
password manager, together with a copy of the file kept somewhere off this
machine. Without the passphrase the file cannot be opened (it is an age file;
`age -d` opens it too). To choose the passphrase instead, or to run init
without a terminal, give it a file (`--bundle-passphrase-file ./pass`, which
may be `/dev/fd/3`) or set `ICHOR_BUNDLE_PASSPHRASE` (at least 12 characters);
with neither and no terminal, init refuses before it does anything.
`--bundle-plaintext` stores the file unencrypted, with a warning; do it only
for a throwaway cluster. [Users and login](../users-and-login.md) and
[Hardening](../hardening.md) say how the file and the passphrase are used later.
It then applies the configuration to the node (which installs Talos and
reboots), bootstraps etcd, saves the kubeconfig next to the bundle, stores the
bundle in the cluster, installs Ichor and waits for Ichor to take over Cilium.
Expect several minutes.

`--merge-kubeconfig` also merges the cluster-admin kubeconfig into your own
(the first existing file of `$KUBECONFIG`, else `~/.kube/config`) as the
context `admin@demo`, and makes it current, so `kubectl` and `ichor` work at
once. Entries already there stay; a name that is taken gets a suffix
(`admin@demo-1`) and init prints the final name.
Without the flag, init leaves your kubeconfig alone and prints the `export
KUBECONFIG=` line to use. The later steps use that line; skip it if you
merged.

## Turn on sign-in (optional, recommended)

Without `--ui-url`, ichor-ui has no sign-in: `ichor init` starts it with
`--allow-unauthenticated` and prints a warning (see
[Users and login](../users-and-login.md)). To use Ichor's own users instead, add
the https URL at which your browser will reach ichor-ui, and a certificate for
it:

```sh
  --ui-url https://192.168.1.60 --admin-user admin \
  --ui-tls-cert ui.crt --ui-tls-key ui.key
```

In the file this is a `ui` block, next to `cluster` and `firstNode`:

```yaml
ui:
  url: https://192.168.1.60
  adminUser: admin
  tls:
    certFile: ./ui.crt
    keyFile: ./ui.key
```

The URL is the issuer of every login. Making it reachable (a LoadBalancer, an
Ingress or a Gateway on ichor-ui's port 8443) is your job; init installs none
of them. At the end init prints the generated password of the first admin to
stderr, once. Save it. Because the kube-apiserver then needs new login
settings, apply them:

```sh
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
ichor rollout config --cluster demo --follow
```

With one control plane the Kubernetes API is down for about a minute while it
restarts.
