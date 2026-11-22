---
sidebar_order: 2
---

# Plan and the secrets bundle

![Adopt compares each machine's live configuration with a fresh Ichor render and sorts every difference into one of three kinds. Representable differences become TalosConfigPatch objects. Ichor-owned ones are listed and need consent. Anything else blocks the machine, which is left out; if every machine is blocked nothing is written.](../../images/adopt-plan-classes.light.drawio.svg)

*Three kinds of difference; only the last one blocks.*

## Step 1: look at the plan

`--plan` reads the cluster and prints what adopt would do. It writes nothing,
not even a Secret.

```sh
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --plan
```

`--cluster` names the `TalosCluster`. `--node` is one control plane; adopt finds
the others from the cluster's membership while the `TalosCluster` does not
exist. If the file holds several clusters, pick one with `--context`.

The plan also prints the Talos version contract it chose (probed from the
running version down to v1.12; below v1.14 is refused), the CNI it found and the
kube-proxy setting. `--version-contract` names one instead.

| Version case | Result |
|---|---|
| Newer Talos or Kubernetes than this Ichor's release set | Adopted as it runs |
| A version the compatibility table does not know | Refused: Ichor could not operate the cluster |
| An incompatible pair (a Kubernetes its Talos does not support, or one the installed Cilium does not run) | A warning; adopted; the upgrade that would fix it is refused until it is compatible |

## Step 2: the secrets bundle

Give adopt the bundle one of two ways (mutually exclusive, one required):

- `--secrets-bundle FILE`: a file from `talosctl gen secrets`, or an encrypted
  one Ichor wrote earlier (adopt asks for its passphrase). A plaintext file
  draws a warning; encrypt it with `ichor secrets encrypt --file FILE`.
- `--recover-secrets --recover-from ADDRESS`: adopt rebuilds the bundle from that
  control plane's machine config, read-only on the node. It is stored under
  `~/.ichor/clusters/<cluster>/` with mode 0600, encrypted like the one
  `ichor init` writes (a generated passphrase printed once on a terminal,
  `--bundle-passphrase-file`, or `ICHOR_BUNDLE_PASSPHRASE`;
  `--bundle-plaintext` opts out).

> [!WARNING]
> Back up the file and its passphrase; adopt does not print the bundle.

A wrong or partial bundle cannot get through: it shows up as secret-field drift,
and no patch is produced for that machine.
