---
sidebar_group: "Trust and recovery"
sidebar_order: 18
---

# Backup, recovery, certificates

![Backup flows from a schedule through snapshot, verify and encrypt to a store outside the cluster; two recovery paths, a live restore and a full-loss restore, read from that store.](../images/backup-recovery-certificates.light.drawio.svg)

*Backups feed two recovery paths; the store must live outside the cluster.*

## Backup

`TalosEtcdBackup` snapshots from a healthy non-leader control plane, verifies and encrypts the snapshot (it holds Secrets) and stores it with retention. `ichor create etcd-backup` writes the object and its credentials Secret; the bucket is the operator's ([Back up etcd to S3](../../guides/etcd-backup.md)). `ichor backup etcd` (Back up now) sets an annotation the controller acts on. Failures and staleness surface as metrics and alerts.

## Recovery

| Path | How | Rules |
|---|---|---|
| Live cluster | `TalosOperation`: stops etcd on all control planes, restores the snapshot | Needs the override right and the cluster's name typed. Progress is kept in a marker next to the backups, not in status, because the API rolls back past the operation. |
| Full loss | `ichor restore cluster` (CLI only): the local bundle and the snapshot rebuild a control plane | The controller then re-reconciles every node as `adopt` does and waits for confirmation before applying anything. Bundle + snapshot must be sufficient. |

The store must be outside the cluster being restored (not `minio.minio.svc`, nor a pod or service address): the marker is written after the API is down, so an in-cluster store deadlocks the restore. The plan refuses it, override does not override that, and `TalosEtcdBackup` carries a `StoreInCluster` warning meanwhile.

## Certificates

- Ichor's own Talos client certificates are short-lived and renewed automatically; an expired one would lock Ichor out with no way back.
- CA rotation (Talos and Kubernetes; etcd and aggregator on request) follows the Talos procedure through the machinery of upgrades. The service-account and secretbox keys rotate in a separate key-rotation operation.
- After every node accepts the new CAs it waits until the operator holds credentials valid for both (written by the CLI, or downloaded in the UI); otherwise the stage that changes what the API servers present would lock the operator out.
- In-cluster clients cannot hold both Kubernetes CAs, so before dropping the old one the controller waits for the new one in every namespace, then a settle delay. Its own client trusts the new CA early through a process-wide trust pool; its leader election outlives the API server restart.
- After the old CAs are dropped and every Node has been Ready for a while, it restarts the clients that keep the old CA, one at a time (CoreDNS, kube-proxy, the CNI, ichor-ui). Other clients that load the CA once may need a restart.
- Expiry dates show in status, the UI and metrics.
