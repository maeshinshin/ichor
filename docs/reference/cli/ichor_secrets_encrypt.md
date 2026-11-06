---
title: "ichor secrets encrypt"
description: "Encrypt a plaintext secrets bundle file in place"
---

## ichor secrets encrypt

Encrypt a plaintext secrets bundle file in place

### Synopsis

encrypt seals a plaintext secrets bundle (ADR 0127) as an age file under a passphrase, replacing the file
atomically and only after the sealed copy has been opened again and found identical. Name the cluster to
encrypt ~/.ichor/clusters/<CLUSTER>/secrets.yaml, or give --file for any other bundle file (for example one
made by 'talosctl gen secrets'). A file that is already encrypted is left alone.

The passphrase comes from --bundle-passphrase-file or $ICHOR_BUNDLE_PASSPHRASE; on a terminal with neither,
one is generated and printed once. The bundle is never printed. The old plaintext may remain on the disk until
its blocks are overwritten; if the file was ever exposed, rotate the CAs.

An encrypted file is a standard age file: 'age -d secrets.yaml' opens it.

```sh
ichor secrets encrypt [CLUSTER] [flags]
```

### Options

```text
      --file string   the bundle file to encrypt, instead of a cluster's
  -h, --help          help for encrypt
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor secrets](ichor_secrets.md)	 - Manage the local secrets bundle file

