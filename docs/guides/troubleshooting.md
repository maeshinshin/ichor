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
- In the UI a warning dialog comes first. An etcd snapshot of any size goes
  straight from the node to your disk; the browser does not hold it.

| Command | Gets | Verb on | Cap |
|---|---|---|---|
| `ichor support-bundle NODE` | A zip per node: kernel log, each service log, service list, process names, mounts, memory, containers, every Talos resource not marked sensitive | `talosmachines/support-bundle` | 256 MiB, 10 min |
| `ichor pcap NODE` | A pcap of one interface | `talosmachines/packet-capture` | 5 min, 100 MiB |
| `ichor snapshot etcd --node CP` | A plaintext etcd snapshot | `talosmachines/etcd-snapshot` | none, ends after 5 min with no data |

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

## Recover a namespace stuck in Terminating

Older versions let `kubectl delete namespace ichor-system` through. The namespace controller then deleted the controller and the webhook Service together with the custom resources, so every delete failed with `failed calling webhook ... service "ichor-webhook-service" not found`. The cluster is unmanaged and the namespace stays `Terminating`. To get out:

1. Save the bundle: `ichor export bundle NAME --bundle-passphrase-file PATH` (`--bundle-plaintext` stores it unencrypted; with no terminal one of the two is required). It reads the TalosCluster and the bundle Secret and writes the file before its last step; that step marks the TalosCluster as exported and fails while the webhook is down. A version without the `ichor-protected-secrets` policy deletes the Secret with the namespace; if the command cannot find the Secret, use a bundle you exported earlier, or give adopt `--recover-secrets --recover-from CP_ADDRESS` below.
2. Delete the validating webhook, so the remaining deletes can pass, a stuck IchorUser among them: `kubectl delete validatingwebhookconfiguration ichor-validating-webhook-configuration`. The mutating one fails open and can stay.
3. The controller is gone, so nothing clears the finalizers. On each TalosMachine, and on each TalosOperation, that has a `deletionTimestamp`: `kubectl -n ichor-system patch talosmachine NAME --type merge -p '{"metadata":{"finalizers":null}}'`. This writes nothing to a node.
4. A version with the `ichor-protected-secrets` policy keeps the Secrets labelled `ichor.maesh.dev/protected` (one secrets bundle per cluster, and the Cilium prior release if adopt took one over). Once the bundles are exported, `kubectl -n ichor-system annotate secret -l ichor.maesh.dev/protected ichor.maesh.dev/allow-delete=true` lets the namespace finish.

5. The IchorInstallation is cluster-scoped and survives the namespace. While it exists, adopt takes Ichor for installed and installs nothing, so delete it: `kubectl delete ichorinstallation ichor`. If you are not adopting again, a manifest install also leaves its webhook configurations, admission policies and ClusterRoles: `kubectl delete -f install.yaml --ignore-not-found` of the release you installed removes them, and the CRDs and every Ichor object left with them.

The nodes keep running, so bring the cluster back under a fresh Ichor with `ichor adopt`. It installs Ichor when it is absent, then creates the bundle Secret, the TalosCluster and the TalosMachines; with no TalosCluster yet, `--node` names one control plane and adopt reads the members from it. Use the file from step 1, or a bundle exported earlier.

adopt reads each node over the Talos API, so it needs a talosconfig that reaches a control plane (`--talosconfig`, else `$TALOSCONFIG`, else `~/.talos/config`). Use your own, or write one from the bundle: `ichor create credentials NAME --bundle ~/.ichor/clusters/NAME/secrets.yaml --kube-server https://CP_ADDRESS:6443 --talos-endpoints CP_ADDRESS --talosconfig-out ./talosconfig`.

A cluster made by an earlier `ichor init` kept the Cilium release record in `ichor-system`, and it was lost with the namespace. adopt then finds Cilium without a record (`CNI: cilium (manifest)`) and reports chart and image differences, so the plan ends with `pass --accept-cilium-diff`. Read the list, then pass the flag on both commands. adopt asks for confirmation before it writes; `--yes` skips the prompt.

```sh
ichor adopt --cluster NAME --node CP_ADDRESS --talosconfig ./talosconfig --secrets-bundle ~/.ichor/clusters/NAME/secrets.yaml --accept-cilium-diff --plan
ichor adopt --cluster NAME --node CP_ADDRESS --talosconfig ./talosconfig --secrets-bundle ~/.ichor/clusters/NAME/secrets.yaml --accept-cilium-diff
```

adopt then turns sign-in on and sets `spec.authentication.ichor`, and a control plane whose kube-apiserver still runs the old install's sign-in entry reports `Ready=False/APIServerRestartRequired`. adopt does not wait for it. It ends with one step to run: `ichor rollout config --cluster NAME --follow`. That one rollout restarts kube-apiserver on each control plane in turn and applies both changes.

With no file at all, `--recover-secrets --recover-from CP_ADDRESS` rebuilds the bundle from that control plane's machine config instead of `--secrets-bundle`. An encrypted file needs its passphrase (`--bundle-passphrase-file` or `$ICHOR_BUNDLE_PASSPHRASE`). An object that already exists is kept when identical, and adopt refuses one that differs. A cluster made with `ichor init` has Ichor's own sign-in entry on its control planes: adopt recognises it, turns sign-in on again and replaces the entry with one for the new install. An identity provider of your own is refused; put it in `spec.authentication.external` first. adopt server-side applies the other cluster-scoped objects of the old install (CRDs, ClusterRoles), as `ichor init` does. `ichor restore cluster` is for a cluster whose control planes and etcd data are gone, not this case.
