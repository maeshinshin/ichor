---
sidebar_order: 8
---

# Rotating the CAs

![The stages of a CA rotation. Accepting, AwaitingCredentials, Issuing, Dropping, Finalizing, Done. AwaitingCredentials waits for the credentials-handoff annotation and changes nothing on a node. Issuing makes three passes, and for etcd reboots each control plane after every pass. Deleting the operation takes it back until Issuing; later it is refused. After Done, run ichor export bundle NAME.](../../images/hardening-rotate-ca.light.drawio.svg)

*A rotation pauses until you hold working credentials for the new CAs.*

`ichor rotate ca NAME` replaces the Talos API and Kubernetes API CAs.
`--ca etcd` and `--ca kubernetes-aggregator` (or `--all`) add the others. The
service-account signing key and the secretbox key are not rotated, so after a
compromise the rotation is complete only for the CAs you list.

## etcd

Talos has no accepted-CA list for etcd, so Ichor puts the old and new CA
certificates into `cluster.etcd.ca` and reboots every control plane, one at a
time and only while etcd is healthy, after each of three passes (Talos does not
restart etcd on a configuration change). That is three reboots per control
plane, each after cordon and drain. With one control plane the Kubernetes API
is gone while it reboots.

Take a snapshot first: set up a scheduled backup once ([Back up etcd to S3](../etcd-backup.md)),
then `ichor backup etcd NAME`. `ichor snapshot etcd --node NODE` needs no setup
but is **plaintext** and not an input to `ichor restore`. Snapshots stay
readable: the backup key does not depend on the CAs.

## Aggregator

The kube-apiserver publishes the CAs it accepts for extension API servers (for
example metrics-server) in `extension-apiserver-authentication`. Ichor waits
until it carries the new CA, then two minutes before the front-proxy
certificate is signed by it. At the end it deletes that ConfigMap once so the
kube-apiserver recreates it with the new CA alone. It restarts no extension API
server: one that reads the CA only once needs a restart by its owner. Needs
`versionContract` v1.14 or later.

## The hand-off

A Talos or Kubernetes CA rotation stops in `AwaitingCredentials` until the
operation carries `ichor.maesh.dev/credentials-handoff`.
`ichor create credentials --acknowledge` and the UI check that the new
kubeconfig works, then set it.

> [!WARNING]
> The controller checks only that the annotation names this rotation's new CAs.
> Setting it by hand with `kubectl annotate` says "I hold a working kubeconfig
> and talosconfig for the new CAs", and nothing verifies it: if you do not, the
> old ones stop working when the old CAs drop.

A rotation of only etcd and the aggregator CA changes nothing your kubeconfig
or talosconfig depends on, so there is no hand-off. After any rotation, run
`ichor export bundle NAME`.

## Abandoning

`ichor delete operation NAME` takes a rotation back before the new CA issues
(Accepting and AwaitingCredentials; Issuing too when neither the Kubernetes nor
the etcd CA is rotated): the new CAs leave the bundle, the cluster lock is
released and the machines render from the old CAs again. The nodes then report
a pending change (the extra accepted CAs, and for etcd the CA field, are still
loaded); `ichor rollout config` drops them and reboots the nodes it must. Later
the delete is refused, because the nodes would sit between two CAs: let it finish.
