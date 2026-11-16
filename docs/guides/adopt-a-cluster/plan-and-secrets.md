---
sidebar_order: 2
---

# Plan and the secrets bundle

## Step 1: look at the plan

`--plan` reads the cluster and prints what adopt would do. It writes nothing,
not even a Secret.

```sh
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --plan
```

`--cluster` is the name the `TalosCluster` will have. `--node` is one control
plane; adopt finds the other machines from the cluster's membership when the
`TalosCluster` does not exist yet. If the file holds several clusters, pick one
with `--context`.

The plan shows, per machine, one of three kinds of difference between the
live config and what Ichor would render:

- Representable (sysctl and kernel-module documents, subnets, install disk).
  Adopt stores these as `TalosConfigPatch` objects.
- Ichor-owned (the Cilium inline manifest entry, KubePrism, kube-proxy when
  replaced, removal of Talos's flannel). Ichor will change these on the first
  render. They are listed and need your consent. They do not block.
- Unrepresentable (anything else in the config, such as another inline
  manifest). That machine is blocked, with the paths and a hint, and is left
  out. If every machine is blocked nothing is written.

It also prints the Talos version contract it chose (probed from the running
version down to v1.12; a contract below v1.14 is refused, as above), the CNI it
found and the kube-proxy setting. The cluster may run a newer Talos or
Kubernetes than this Ichor's release set; adopt takes it as it runs. A version the compatibility table does not know at
all is refused, since Ichor could not operate the cluster. An incompatible pair
(a Kubernetes its Talos does not support, or one the installed Cilium does not
run) is reported as a warning and adopted; the upgrade that would make it
compatible is refused until it is. `--version-contract` is probed
from the running Talos unless you name one.

## Step 2: the secrets bundle

Give adopt the bundle in one of two ways (they are mutually exclusive, and one
is required):

- `--secrets-bundle FILE`: a file produced by `talosctl gen secrets`, or an
  encrypted one Ichor wrote earlier (adopt asks for its passphrase, see below).
  A plaintext file works but draws a warning: encrypt it with
  `ichor secrets encrypt --file FILE`.
- `--recover-secrets`: adopt rebuilds the bundle from a control plane's machine
  config. Read-only on the node. `--recover-from ADDRESS` names that control
  plane (required with `--recover-secrets`). The bundle is stored under
  `~/.ichor/clusters/<cluster>/` with mode 0600, encrypted like the one
  `ichor init` writes (a generated passphrase printed once on a terminal,
  `--bundle-passphrase-file`, or `ICHOR_BUNDLE_PASSPHRASE`;
  `--bundle-plaintext` opts out).

> [!WARNING]
> Back up the file and its passphrase; adopt does not print the bundle.

A wrong or partial bundle cannot get through. It shows up as secret-field
drift, and no patch is produced for that machine.
