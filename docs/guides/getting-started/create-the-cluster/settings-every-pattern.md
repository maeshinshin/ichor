---
sidebar_order: 4
---

# Settings every pattern shares

Every pattern runs the same `ichor init`, so the settings on this page work with
each of them. The pattern page gives the endpoint and the first node's settings.

## The shape of the cluster

A fresh install runs Talos v1.14.5 with Kubernetes v1.36.6, under
Talos config contract `v1.14`. `ichor init` refuses other versions on a fresh
install.

`--preset` is `standard` by default: three control planes, with workers added
later and tainted. `single` is one schedulable node, `compact-ha` is three
schedulable control planes, and `large` is five control planes with workers
added later.

On virtual networks that pass geneve but not vxlan, add `--cni-tunnel geneve`
for Cilium. Cilium uses vxlan by default.

`--gateway-api` and `--l2-announcements` (with `--l2-pool`) turn on Cilium's
Gateway API and a LoadBalancer address on the LAN. Both need `--kube-proxy replaced`
(Cilium replaces kube-proxy), which is refused otherwise. The
[LAN load balancers](../../lan-load-balancers.md) guide has the file keys and what
you write yourself.

The pod and service subnets (`--pod-subnets` and `--service-subnets`, by
default `10.244.0.0/16` and `10.96.0.0/12`) must not overlap the network the
nodes sit on, and they cannot change once the cluster exists.

`--registry-mirror HOST=URL` writes an anonymous mirror into the machine config
(`spec.registries`), for nodes that cannot reach a registry directly. It is
repeatable and not needed in most networks.

The install disk can be given as `wwid:ID`, `serial:SERIAL`, a
`/dev/disk/by-id/` path or a device such as `/dev/sda`.

`ichor init --help` lists the rest, including the disk selectors and the
discovery addresses.

## Certificates for the endpoint

Ichor does not configure this; Talos adds the endpoint to the certificate it
issues on each control plane. So `kubectl` verifies against the endpoint with
every pattern, including a name for `loadBalancer`, and you do not
need to add the endpoint to any certificate setting.

## The init.yaml file

`ichor init -f init.yaml` reads the same settings from a file. `ichor init
--print-config-template` prints a commented example. A flag on the command line
overrides the file. Paths in the file (the TLS certificate and key on the sign-in
step below) are relative to the file, and the file never holds the key itself,
only its path.

## The secrets bundle

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
for a throwaway cluster. [Users and login](../../users-and-login.md) and
[Hardening](../../hardening.md) say how the file and the passphrase are used later.

Then init applies the configuration to the node (which installs Talos and
reboots), bootstraps etcd, saves the kubeconfig next to the bundle, stores the
bundle in the cluster, installs Ichor and waits for Ichor to take over Cilium.
Expect several minutes.

## Your kubeconfig

`--merge-kubeconfig` also merges the cluster-admin kubeconfig into your own
(the first existing file of `$KUBECONFIG`, else `~/.kube/config`) as the
context `admin@demo`, and makes it current, so `kubectl` and `ichor` work at
once. Entries already there stay; a name that is taken gets a suffix
(`admin@demo-1`) and init prints the final name.

Without the flag, init leaves your kubeconfig alone and prints the `export
KUBECONFIG=` line to use. The later steps use that line; skip it if you
merged.

## Sign-in

Ichor's own sign-in is on by default: init creates the first admin (`admin`, or
`--admin-user`), prints its generated password to stderr once, and the first
login forces a new one. Without `--ui-url` the issuer is `https://localhost:8443`,
where `ichor ui` serves ichor-ui. To publish ichor-ui at an address of your own,
give the https URL at which your browser will reach it, and a certificate for
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

To run ichor-ui with no sign-in, pass `--allow-unauthenticated`. Then anyone who
can reach ichor-ui can change the cluster with ichor-ui's permissions, including
resetting nodes, and init prints a warning saying so. There is no sign-in
step, admin or rollout in that case.
