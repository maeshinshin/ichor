---
sidebar_group: "Trust and recovery"
sidebar_order: 18
---

# Backup, recovery, certificates

![A backup runs from a schedule or Back up now, takes a snapshot from a healthy non-leader control plane, verifies and encrypts it, and stores it in an S3-compatible store outside the cluster. Recovery has two paths from that store: a live-cluster restore as a TalosOperation, and a full-loss restore by the CLI. Certificate rules sit below.](../images/backup-recovery-certificates.light.drawio.svg)

*Backups feed two recovery paths; the store must live outside the cluster.*

## Backup

`TalosEtcdBackup` snapshots from a healthy non-leader control plane, verifies and encrypts the snapshot (it contains Secrets) and stores it with retention. `ichor create etcd-backup` writes the object and its credentials Secret; the bucket is the operator's ([Back up etcd to S3](../../guides/etcd-backup.md)). `ichor backup etcd` (Back up now on the Backups page) sets an annotation the controller acts on. Failures and staleness surface as metrics and alerts.

## Recovery

| Path | How | Rules |
|---|---|---|
| Live cluster | `TalosOperation` (UI or CLI): stops etcd on all control planes, restores the snapshot | Needs the override right on the cluster and the cluster's name typed. Progress lives in a marker next to the backups, not in status, because the restore rolls the API back past the operation; the operation is recreated from the marker. |
| Full loss | `ichor restore cluster` (CLI only): rebuilds control planes from the local bundle and the snapshot | The controller then re-reconciles every node as `adopt` does and waits for confirmation before applying anything, since the restored CRs may not match the hardware. Bundle + snapshot must be sufficient. |

The backup store must be outside the cluster being restored: not a Service such as `minio.minio.svc`, nor an address in the pod or service range. The marker is written after the API is down, so an in-cluster store deadlocks the restore. The plan refuses it, override does not override that, and the `TalosEtcdBackup` carries a `StoreInCluster` warning condition meanwhile.

## Certificates

- Ichor's own Talos client certificates are short-lived and renewed automatically; an expired one would lock Ichor out with no way back.
- CA rotation (Talos and Kubernetes by default, the etcd and aggregator CAs on request) follows the documented Talos procedure and runs through the machinery of upgrades. The service-account and secretbox keys rotate in a separate key-rotation operation.
- After every node accepts the new CAs it waits until the operator has credentials that work with both CAs; otherwise the stage that changes what the API servers present would lock the operator out. The CLI writes the new credentials, or an administrator downloads them in the UI.
- In-cluster clients cannot hold both Kubernetes CAs (`kube-root-ca.crt` switches at Issuing). Before dropping the old CA the controller waits for the new one in every namespace, then a settle delay. Its own client trusts the new CA ahead of the switch through a process-wide trust pool, and its leader election outlives the API server restart.
- After the old CAs are dropped and every Node has been Ready for a while, it restarts the clients that keep the old CA, one at a time (CoreDNS, kube-proxy, the CNI, ichor-ui). Other clients that load the CA once may still need a restart.
- Expiry dates are visible in status, the UI and metrics.
