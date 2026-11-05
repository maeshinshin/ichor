---
title: "ichor cluster rotate-ca"
description: "Replace the cluster's certificate authorities (Talos API, Kubernetes API, and optionally etcd and aggregator)"
---

## ichor cluster rotate-ca

Replace the cluster's certificate authorities (Talos API, Kubernetes API, and optionally etcd and aggregator)

### Synopsis

rotate-ca starts a CA rotation operation: the Talos API CA and the Kubernetes API CA are
replaced in passes over every machine (accept the new CA, make it the issuer, drop the old one),
each verified before the next. The kubelet restarts on each machine in the passes that change the
Kubernetes CA.

--ca chooses the CAs (talos, kubernetes, etcd, kubernetes-aggregator; repeat the flag or separate
with commas), --all takes the four. Without either, the Talos API and Kubernetes API CAs are
replaced. The etcd CA is replaced by rebooting every control plane, one at a time, after each of the
three passes (Talos neither restarts etcd on a configuration change nor lets an API call do it): each
node is cordoned and drained, hands etcd leadership over, reboots, and the next one waits until it is
back, Ready and etcd is healthy; with one control plane the Kubernetes API is unavailable during each
reboot. That is three reboots of every control plane. Take an etcd snapshot first ('ichor etcd backup
now NAME'). Deleting the operation before the new CA issues abandons the rotation (the old
configuration is rendered again); 'ichor config apply' then drops what the nodes still accept. The
aggregator CA needs the extension API servers (metrics-server, for one) to load the new CA: Ichor
waits for kube-apiserver to publish it and restarts none of them. A rotation of etcd and the
aggregator CA alone changes nothing your kubeconfig or talosconfig depends on, so it has no
credential hand-off. The service-account signing key and the secretbox key are not rotated.

When the Talos API or Kubernetes API CA is replaced, your kubeconfig and talosconfig STOP WORKING from the second pass (Issuing) on: the nodes start serving certificates from the new CA, and a file that trusts only the old CA can no longer verify them. So after every
machine accepts the new CAs, the operation WAITS, changing nothing, until you have new credentials.
This command waits for that point while your current credentials still work, then writes

  a kubeconfig and a talosconfig that trust the old and the new CAs and are signed by the new one
  (next to your current ones, with the suffix .ichor-new; your current files are not
  touched; mode 0600),

checks that the new kubeconfig authenticates, and only then lets the operation go on. The hand-off
needs your own kubeconfig, so this command does it with or without --follow: it stays until the
operation has gone on (a few seconds to a few minutes), then returns, and the rest of the rotation
runs in the controller. The files' contents are never printed. If this command is interrupted while the operation waits, run
'ichor cluster credentials NAME --acknowledge' with your current kubeconfig. If you lost both,
'ichor cluster credentials NAME --bundle FILE' makes credentials from a current bundle file.

Before it starts, every machine must answer, run the configuration Ichor renders for it, and every
Kubernetes Node must be Ready; otherwise the operation pauses without changing anything. While it
runs, the machine controller does not reconcile the cluster's machines and other disruptive
operations are refused. It resumes by itself after the controller restarts, and does not advance past
a step it could not verify. Machines held with upgrade-hold are not skipped.

When it completes, the secrets bundle in the cluster has been replaced, and the copy saved on your
machine is stale: run 'ichor cluster bundle-export NAME' and keep the file safe.

It shows which machines are reconfigured and in which order, and asks for confirmation unless --yes.
--follow keeps reporting progress until the rotation completes, through the Kubernetes API with the
new kubeconfig (the ichor-ui tunnel does not survive the rotation).

```
ichor cluster rotate-ca NAME [flags]
```

### Options

```
      --all          replace all four CAs
      --ca strings   the CAs to replace: talos, kubernetes, etcd, kubernetes-aggregator (default: talos and kubernetes)
      --follow       after the credential hand-off, follow the rotation until it completes or fails (without it the command still does the hand-off, then returns)
  -h, --help         help for rotate-ca
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor cluster](ichor_cluster.md)	 - List or inspect clusters

