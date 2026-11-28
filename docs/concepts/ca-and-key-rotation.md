---
title: "CA and key rotation"
description: "How Ichor replaces the cluster CAs and the service-account and secretbox keys without locking you out: verified passes, the credential hand-off and the dual-key period."
sidebar_group: "Changes over time"
sidebar_order: 50
---

# CA and key rotation

Each rotation is a [`TalosOperation`](../reference/crds/talosoperation.md), so it
follows [Operations](operations.md): one cluster lock, pause on a failed step,
`resume` to retry. Starting one needs a verb only `ichor-admin` holds
([Security model](security.md)).

| | CA rotation | Key rotation |
|---|---|---|
| Command | `ichor rotate ca NAME` | `ichor rotate keys NAME` |
| Kind | `ca-rotation` | `key-rotation` |
| Verb | `start` on `talosoperations/ca-rotation` | `start` on `talosoperations/key-rotation` |
| Replaces | Talos API and Kubernetes API CAs; `--ca` or `--all` add the etcd and aggregator CAs | service-account signing key and secretbox (encryption at rest) key |
| Changes your credentials | yes | no |

Take a snapshot first. A scheduled backup ([Back up etcd to
S3](../guides/etcd-backup.md)) is encrypted and `ichor restore` reads it;
`ichor snapshot etcd --node NODE` needs no setup but writes **plaintext** (every
Secret) to your disk, and `ichor restore` does not take that file.

## CA rotation

Three passes over every machine, each verified on every node before the next:
accept the new CA, make it the issuer, drop the old one. The etcd members load
the new CA by a reboot, one control plane at a time, with a health check before
each.

![The stages of a CA rotation: Accepting, AwaitingCredentials, Issuing, Dropping, Finalizing and Completed, with what each does to the nodes. Until Issuing the old CA signs, both CAs are trusted and the old kubeconfig and talosconfig still work; during AwaitingCredentials the operator takes the new credentials. From Issuing the new CA signs and only the new credentials work. Deleting the operation before Issuing abandons the rotation; from Issuing the deletion is held until the rotation is done, and once it is Completed deleting removes only the record.](../images/ca-rotation.light.drawio.svg)

*Deleting the operation abandons a rotation until Issuing, the point of no return for the default targets (the Talos and Kubernetes CAs); after that the deletion waits for the rotation to finish.*

### The credential hand-off

From the second pass a kubeconfig or talosconfig that trusts only the old CA
stops working. So the operation stops in `AwaitingCredentials` until you hold
credentials that work with both CAs.

- CLI: `ichor rotate ca` waits, writes a kubeconfig and talosconfig next to
  yours with the suffix `.ichor-new` (yours are untouched), checks that the new
  kubeconfig authenticates, then lets the operation go on.
  `ichor create credentials NAME --acknowledge` does the same for a rotation
  already waiting.
- UI: an administrator with `download` on `talosclusters/credentials` downloads
  them and presses continue. Each download leaves a Warning Event.

The controller continues only when the operation carries the annotation
`ichor.maesh.dev/credentials-handoff` with the new CAs' fingerprints. It never
writes `spec`. Rotating only the etcd and aggregator CAs changes nothing your
credentials depend on, so it has no hand-off.

### In-cluster clients

Clients cannot hold both Kubernetes CAs. Before dropping the old one the
controller waits until the new one is in every namespace, then a settle delay.
Once every Node has been Ready for a while it restarts, one at a time, the
clients that keep the old CA, in this order: the CNI (Cilium's agents, then its
operator; skipped when the CNI is unmanaged), then kube-proxy, CoreDNS and
ichor-ui. Others
that load the CA once may still need a restart. CA expiry dates show in status,
the UI and metrics.

## Key rotation

A dual-key period: the old key keeps verifying or decrypting until nothing
depends on it. Stages are `Accepting`, `Issuing`, `Reencrypting`,
`SettlingTokens`, `Dropping` and `Finalizing`; stages that do not apply are
skipped. Control planes are reconfigured one at a time, waiting for each
kube-apiserver. Nothing reboots and your credentials keep working.

- Every Secret is rewritten under the new secretbox key. The old
  service-account key stays while pods refresh tokens (`--token-settle`, 90
  minutes by default; under 75 is refused).
- The etcd backup key is versioned, so older snapshots still decrypt. Restoring
  a snapshot older than a finished rotation is refused.

Guide: [Rotating the service-account and secretbox keys](../guides/rotate-keys.md).
Commands: [`ichor rotate ca`](../reference/cli/ichor_rotate_ca.md),
[`ichor rotate keys`](../reference/cli/ichor_rotate_keys.md),
[`ichor create credentials`](../reference/cli/ichor_create_credentials.md).
