---
title: "ichor ui"
description: "Open the Ichor UI through a port-forward to ichor-ui"
---

## ichor ui

Open the Ichor UI through a port-forward to ichor-ui

### Synopsis

ui opens a port-forward to one of the ichor-ui Deployment's pods (found
through the ichor-ui Service (found by its component label), never a hardcoded pod name) and opens
it in your default browser.

If the pod currently forwarded to disappears -- a rollout, a drain, an
eviction -- ui re-establishes the tunnel to another ready pod on the same
local port automatically, printing what it is doing rather than hanging
silently. Press Ctrl-C to stop.

```sh
ichor ui [flags]
```

### Examples

```sh
  ichor ui
  ichor ui --local-port 8080 --open-browser=false
```

### Options

```text
  -h, --help               help for ui
      --local-port int     local port to forward to (defaults to the Service's own port)
  -n, --namespace string   namespace ichor-ui runs in (default "ichor-system")
      --open-browser       open the default web browser once the port-forward is ready (default true)
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

