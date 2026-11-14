---
title: "CA and key rotation"
description: "How Ichor replaces the cluster CAs and the service-account and secretbox keys without locking you out: verified passes, the credential hand-off and the dual-key period."
sidebar_group: "Changes over time"
sidebar_order: 50
---

# CA and key rotation

Ichor rotates two kinds of cluster secrets, each as a
[`TalosOperation`](../reference/crds/talosoperation.md) of its own kind, so each
follows the rules of [Operations](operations.md): one cluster lock, pause on a
failed step, `resume` to run the step again. Starting either needs a verb that
only `ichor-admin` holds ([Security model](security.md)). Take an etcd snapshot
first (`ichor backup etcd NAME`).

| | CA rotation | Key rotation |
|---|---|---|
| Kind | `ca-rotation` | `key-rotation` |
| Command | `ichor rotate ca NAME` | `ichor rotate keys NAME` |
| Verb | `start` on `talosoperations/ca-rotation` | `start` on `talosoperations/key-rotation` |
| Replaces | Talos API CA and Kubernetes API CA; with `--ca` or `--all` also the etcd CA and the aggregator CA | Service-account signing key and the secretbox (encryption at rest) key |
| Changes your credentials | Yes | No |

## CA rotation

The CAs are replaced in three passes over every machine: accept the new CA,
make it the issuer, drop the old one. Each pass is verified on every node
before the next starts.
By default the Talos API and Kubernetes API CAs are replaced.
`--ca etcd`, `--ca kubernetes-aggregator` or `--all` add the etcd CA and the
aggregator CA.
The etcd members load the new CA by a reboot, one control plane at a time with a
health check before each.

![The stages of a CA rotation: Accepting, AwaitingCredentials, Issuing, Dropping, Finalizing and Completed, with what each does to the nodes. Until Issuing the old CA signs, both CAs are trusted and the old kubeconfig and talosconfig still work; during AwaitingCredentials the operator takes the new credentials. From Issuing the new CA signs and only the new credentials work. Deleting the operation before Issuing abandons the rotation; from Issuing the deletion is held until the rotation is done, and once it is Completed deleting removes only the record.](../images/ca-rotation.light.drawio.svg)

*Deleting the operation abandons a rotation until Issuing, the point of no return for the default targets (the Talos and Kubernetes CAs); after that the deletion waits for the rotation to finish.*

### The credential hand-off

From the second pass the nodes serve certificates from the new CA, and a
kubeconfig or talosconfig that trusts only the old CA stops working. Ichor
avoids locking you out by stopping, after every node accepts the new CAs, in the
stage `AwaitingCredentials`, where it changes nothing until you hold
credentials that work with both CAs.

- With the CLI, `ichor rotate ca` waits for that point, writes a
  kubeconfig and talosconfig next to yours with the suffix `.ichor-new` (yours
  are not touched), checks that the new kubeconfig authenticates, and only then
  lets the operation go on. `ichor create credentials NAME --acknowledge` does
  the same for a rotation already waiting.
- In the UI, an administrator who holds `download` on `talosclusters/credentials`
  downloads the credentials and presses continue.
  Each download leaves a Warning Event first.

The controller continues only when the operation carries the annotation
`ichor.maesh.dev/credentials-handoff` with the fingerprints of the new CAs. It
reads the annotation and never writes `spec`. A rotation of the etcd and
aggregator CAs alone changes nothing your credentials depend on, so it has no
hand-off.

If you delete the operation before the new CA starts issuing, the rotation is
abandoned and the old configuration is rendered again.

### In-cluster clients

In-cluster clients cannot hold both Kubernetes CAs, so before it drops the old
CA the controller waits until the new one is in every namespace, then a settle
delay. After the old CAs are gone and every Node has been Ready for a while, it
restarts, one at a time, the clients that keep the old one: CoreDNS,
kube-proxy, the CNI and ichor-ui. Other in-cluster clients that load the CA
once may still need a restart. Expiry dates of the CAs are visible in status,
in the UI and in metrics.

## Key rotation

`ichor rotate keys` replaces the service-account signing key and the
secretbox key. Both need a dual-key period, in which the old key keeps
verifying or decrypting until nothing depends on it. The stages are
`Accepting`, `Issuing`, `Reencrypting`, `SettlingTokens`, `Dropping` and
`Finalizing`, and a stage that does not apply to the keys you chose is skipped.
Control planes are reconfigured one at a time and the operation waits for each
kube-apiserver to answer. Nothing reboots, and your kubeconfig and talosconfig
keep working.

Two things to know:

- Every Secret is rewritten under the new secretbox key, and the old
  service-account key is kept while pods refresh their tokens
  (`--token-settle`, 90 minutes by default, and less than 75 minutes is
  refused).
- The etcd backup key is versioned, so snapshots sealed before a rotation still
  decrypt. A restore of a snapshot older than a finished rotation is refused.

The step-by-step guide is [Rotating the service-account and secretbox
keys](../guides/rotate-keys.md). Commands:
[`ichor rotate ca`](../reference/cli/ichor_rotate_ca.md),
[`ichor rotate keys`](../reference/cli/ichor_rotate_keys.md),
[`ichor create credentials`](../reference/cli/ichor_create_credentials.md).
