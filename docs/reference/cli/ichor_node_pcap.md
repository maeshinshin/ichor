---
title: "ichor node pcap"
description: "Capture packets on a node's interface into a pcap file, for a bounded time and size"
---

## ichor node pcap

Capture packets on a node's interface into a pcap file, for a bounded time and size

### Synopsis

pcap captures packets on one interface of a node and downloads them as a pcap file. The
capture ends after --duration (default 30s, at most 5m) or --max-size (at most 100 MiB), whichever
comes first, and the file is complete either way. Packets on the wire can contain secrets.

--filter takes a classic BPF program in "tcpdump -dd" format (the form Talos takes), or @FILE to
read it from a file, for example:
  ichor node pcap worker-1 --interface eth0 --filter "$(tcpdump -dd -y EN10MB 'tcp port 80')" -o web.pcap

Needs the "download" verb on talosmachines/packet-capture. The file is written with mode 0600.

```
ichor node pcap NODE [flags]
```

### Options

```
      --duration duration   capture time (at most 5m) (default 30s)
      --filter string       BPF filter in "tcpdump -dd" format, or @FILE
  -h, --help                help for pcap
      --interface string    interface to capture on (default "eth0")
      --max-size int        stop after this many bytes (default and maximum 100 MiB)
  -o, --output string       file to write (mode 0600); default the name the server suggests; - for standard output
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor node](ichor_node.md)	 - Show a node, tail its logs (needs ichor-ui), or reboot, shut down, reset or remove it

