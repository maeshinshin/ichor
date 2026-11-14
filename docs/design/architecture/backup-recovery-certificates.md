---
sidebar_group: "Trust and recovery"
sidebar_order: 18
---

# Backup, recovery, certificates

`TalosEtcdBackup` takes snapshots on a schedule from a healthy non-leader
control plane, verifies them, encrypts them (snapshots contain Secrets) and
stores them in S3-compatible storage with retention. A snapshot can also be
taken outside the schedule (`ichor backup etcd`, Back up now on the Backups
page), which sets an annotation the controller acts on. Failures and staleness
surface as metrics and alerts.

Two recovery paths. Restoring into a live cluster stops etcd on all control
planes and restores the snapshot; the UI and CLI both offer it, as a
`TalosOperation` that needs the override right on the cluster and the
cluster's name typed. Because the restore rolls the Kubernetes API back past the
operation itself, its progress is kept in a marker next to the backups, not in
status, and the operation is recreated from the marker. The backup
store must therefore live outside the cluster being restored (not a Service
such as `minio.minio.svc`, nor an address in the cluster's pod or service
range): the marker is written to the store after the API is down, so a store
inside the cluster deadlocks the restore. The plan refuses it, override does
not override that, and the `TalosEtcdBackup` carries a `StoreInCluster`
warning condition meanwhile. Full-loss
recovery is CLI-only: `ichor restore cluster` rebuilds control
planes from the local bundle and the snapshot, after which the controller
re-reconciles every node the way `adopt` does and waits for confirmation
before applying anything, since the restored CRs may not match the hardware.
The goal is that bundle + snapshot is sufficient.

Ichor's own Talos client certificates are short-lived and renewed
automatically; an expired one would lock Ichor out with no way back. CA
rotation (Talos and Kubernetes by default, the etcd and aggregator CAs on request; the service-account key and the secretbox key by a separate key-rotation operation) follows the documented Talos procedure and
runs through the same machinery as upgrades, except that it waits, after every
node accepts the new CAs, until the operator has credentials that work with
both CAs; otherwise the first stage that changes what the API
servers present would lock the operator out. The new credentials are written by
the CLI or downloaded by an administrator in the UI. In-cluster
clients cannot hold both Kubernetes CAs (`kube-root-ca.crt` switches at
Issuing), so before dropping the old CA the controller waits for the new one in
every namespace and then a settle delay. Its own client trusts the new
Kubernetes CA ahead of the switch through a process-wide trust pool, and its
leader election outlives the API server restart. After the old CAs are dropped
and every Node has been Ready for a while, it restarts, one at a time, the
clients that keep the old CA (CoreDNS, kube-proxy, the CNI, ichor-ui); other
in-cluster clients that load the CA once may still need a restart.
Expiry dates are visible in
status, the UI and metrics.
