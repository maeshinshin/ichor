# Investigate a node: logs, packet capture, support bundle

Start with what you can read, then download only what you need. Reading a node
changes nothing. The three downloads can hold secrets, so each needs the
`download` verb and leaves an Event.

![Four ways to look at a node. ichor-ui asks the controller for a short-lived certificate and talks to the node. Logs and live state are read only. A support bundle, a packet capture and an etcd snapshot each need the download verb and record a Warning Event on the node.](../images/troubleshoot-node.light.drawio.svg)

*Read first (blue), download last (amber).*

## 1. Read the logs

```sh
ichor logs node worker-3
ichor logs node worker-3 --service kubelet --follow
ichor logs node cp-1 cp-2 cp-3 --service etcd
```

| Flag | Meaning |
|---|---|
| `--service NAME` | A Talos service (`kubelet`, `etcd`, ...). Empty means the kernel log. |
| `--follow` | Keep streaming. After a dropped connection it resumes from the last resume token; a few lines around the cut may repeat or be missed. |
| `--idle-timeout D` | Without `--follow`, also stop after this long with no new line. Off by default; for an ichor-ui older than the CLI. |
| `-o json` | One JSON object per line. With several nodes, plain output is prefixed with the node name. |

One request names at most 32 nodes.

In the UI, the **Logs** tab of a node reads the kernel log or `kubelet`, `etcd`,
`trustd`, `apid` or `machined`. It keeps the newest 2000 lines and can pause,
wrap long lines and copy what is on screen.

## 2. Read the live state

The node page reads the node as it is now, with no download:

| Tab | Shows |
|---|---|
| Storage & network | Addresses, gateways, DNS, links; disks and volumes |
| Talos | Version, platform, stage, SecureBoot, kernel arguments (a value that could hold a credential is hidden), services and their health, extensions; the heaviest processes (never their command lines), containers, the kernel log |
| Kubernetes & etcd | The Kubernetes node, its taints and pods; etcd health, member and leader IDs, database size, Raft state, alarms and members; routes, discovery and time |

A node that is not approved yet is read over the limited maintenance API; some
sections stay empty until you approve it.

## 3. Download what the reads do not show

All three commands share these rules:

- The file is written with mode 0600. `--out FILE` names it, `--out -` writes to
  standard output.
- They need `ichor-ui`. `--local` uses your own kubeconfig's RBAC instead; a
  refusal says so.
- At most two downloads run at once.
- In the UI a warning dialog comes first. The browser holds the whole file in
  memory and refuses one over 1 GiB; the CLI has no such limit.

| Command | Gets | Verb on | Cap |
|---|---|---|---|
| `ichor support-bundle NODE` | A zip per node: kernel log, each service log, service list, process names, mounts, memory, containers, every Talos resource not marked sensitive | `talosmachines/support-bundle` | 256 MiB, 10 min |
| `ichor pcap NODE` | A pcap of one interface | `talosmachines/packet-capture` | 5 min, 100 MiB |
| `ichor snapshot etcd --node CP` | A plaintext etcd snapshot | `talosmachines/etcd-snapshot` | 8 GiB, 30 min |

### Support bundle

```sh
ichor support-bundle worker-3
ichor support-bundle --cluster prod --out prod-support.zip
```

`--cluster` takes every approved node. The bundle leaves out the machine
configuration, certificates and keys. Logs are free text and can still contain a
credential a program printed.

### Packet capture

```sh
ichor pcap worker-3 --duration 10s
ichor pcap worker-1 --interface eth0 --filter "$(tcpdump -dd -y EN10MB 'tcp port 80')" --out web.pcap
```

| Flag | Default | Limit |
|---|---|---|
| `--interface` | `eth0` | Name of 1 to 32 characters |
| `--duration` | 30s | 5m |
| `--max-size` | 100 MiB | 100 MiB |
| `--filter` | none | A classic BPF program in `tcpdump -dd` format, or `@FILE`; at most 4096 instructions |

The capture ends at the first limit reached and the file is complete either
way. Packets on the wire can hold passwords and tokens.

### Etcd snapshot

```sh
ichor snapshot etcd --node cp-1 --out etcd.snapshot
```

The snapshot holds every Kubernetes Secret and is not encrypted. For a scheduled,
encrypted backup use [a `TalosEtcdBackup`](etcd-backup.md).

## Who may download, and the audit trail

- The verb is `download` on `talosmachines/support-bundle`, `etcd-snapshot` and
  `packet-capture`. The default holder is `admin`
  ([Security](../concepts/security.md#roles)).
- Each download records a Warning Event on the `TalosMachine`, `DiagnosticsDownload`,
  naming who asked and when. If the Event cannot be recorded, the download is
  refused.
- A node that is not approved has no Talos credentials, so it cannot be
  downloaded from.

Reference: [`ichor logs node`](../reference/cli/ichor_logs_node.md),
[`ichor support-bundle`](../reference/cli/ichor_support-bundle.md),
[`ichor pcap`](../reference/cli/ichor_pcap.md),
[`ichor snapshot etcd`](../reference/cli/ichor_snapshot_etcd.md).
