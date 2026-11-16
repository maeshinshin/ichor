---
sidebar_order: 8
---

# Rotating the CAs

`ichor rotate ca NAME` replaces the Talos API and Kubernetes API CAs by
default. `--ca etcd` and `--ca kubernetes-aggregator` (or `--all`) add the other
two. The service-account signing key and the secretbox key are not
rotated, so rotating after a compromise is complete only for the CAs
you list.

## etcd

Talos has no accepted-CA list for etcd, so Ichor puts the old and the
new CA certificate into `cluster.etcd.ca` together and reboots every control
plane, one at a time and only while etcd is healthy, after each of the three
passes (Talos does not restart etcd on a configuration change and its etcd
service has no restart call, so a reboot is the only way a member loads the
CA): three reboots of every control plane, each preceded by cordon and drain.
A cluster with one control plane has no Kubernetes API while its only member
reboots. Take a snapshot first: set up a scheduled backup once
([Back up etcd to S3](../etcd-backup.md)), then `ichor backup etcd NAME`.
(`ichor snapshot etcd --node NODE` needs no setup but is **plaintext** and
not an input to `ichor restore`; see that page.) Snapshots stay readable: the backup key does
not depend on the CAs.

## Aggregator

kube-apiserver publishes the CAs it accepts for extension API
servers (for example metrics-server) in `extension-apiserver-authentication`.
Ichor waits until it carries the new CA and then two minutes before the
front-proxy certificate is signed by it. When the rotation ends it deletes
`extension-apiserver-authentication` once, so that kube-apiserver recreates it
with the new CA alone (it never removes an old CA from that ConfigMap by
itself). It restarts none of the extension API servers: one that reads the CA
only once needs a restart by its owner.
The aggregator CA needs `versionContract` v1.14 or later.

## No hand-off

A rotation of etcd and the aggregator CA alone changes nothing
your kubeconfig or talosconfig depends on, so it does not wait for new
credentials. After any rotation run `ichor export bundle NAME`.

## The hand-off annotation is an attestation

> [!WARNING]
> A rotation of the Talos or Kubernetes
> CA stops in `AwaitingCredentials` until the operation carries the annotation
> `ichor.maesh.dev/credentials-handoff`. `ichor create credentials --acknowledge`
> and the UI check that the new kubeconfig works against the cluster, and only
> then set it. The controller checks only that the annotation names this
> rotation's new CAs, so setting it by hand with `kubectl annotate` says "I hold a
> working kubeconfig and talosconfig for the new CAs" and nothing verifies it: if
> you do not, the old ones stop working when the old CAs drop.

## Abandoning

`ichor delete operation NAME` on a rotation that has not yet
made the new CA issue (Accepting and AwaitingCredentials; Issuing too when
neither the Kubernetes nor the etcd CA is rotated) takes it back: the new CAs
leave the bundle, the cluster lock is released and the machines are rendered
from the old CAs again. The nodes then report a pending change (the extra
accepted CAs, and for etcd the CA field, are still loaded); run
`ichor rollout config` to drop them, which reboots the nodes it has to. Later in
the rotation the delete is refused, because the nodes would be left between
two CAs: let it finish.
