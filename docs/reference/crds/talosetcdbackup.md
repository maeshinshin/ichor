---
title: "TalosEtcdBackup"
description: "TalosEtcdBackup is the Schema for the talosetcdbackups API"
---

# TalosEtcdBackup

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosEtcdBackup is the Schema for the talosetcdbackups API

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosEtcdBackup` (list kind `TalosEtcdBackupList`) |
| Resource | `talosetcdbackups` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of TalosEtcdBackup |
| `spec.clusterRef` | `object` | yes | clusterRef names the TalosCluster (same namespace) whose etcd is backed up. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.destination` | `object` | yes | destination is where snapshots are stored. |
| `spec.destination.s3` | `object` | yes | s3 is an S3-compatible bucket. |
| `spec.destination.s3.bucket` | `string` | yes | bucket is the bucket name. It must already exist. |
| `spec.destination.s3.credentialsSecretRef` | `object` | yes | credentialsSecretRef names a Secret holding accessKeyID and secretAccessKey (and optionally sessionToken, and ca.crt, a PEM bundle that signs the endpoint). It is read from the controller's own namespace, not the namespace of this object. |
| `spec.destination.s3.credentialsSecretRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.destination.s3.endpoint` | `string` | yes | endpoint is the service URL, e.g. https://s3.eu-west-1.amazonaws.com or http://minio.minio.svc:9000. The scheme selects TLS. |
| `spec.destination.s3.insecure` | `boolean` | no | insecure permits a plain http:// endpoint. The access key id, the signed headers and the session token then cross the network in clear, so the admission webhook refuses http:// unless this is true. Changing it needs update on talosetcdbackups/secret-references, like the endpoint. Unset (false) means https only. |
| `spec.destination.s3.prefix` | `string` | yes | prefix is the key prefix snapshots are written under, with no leading or trailing slash. Snapshots of different clusters need different prefixes. |
| `spec.destination.s3.region` | `string` | no | region is the bucket's region; empty lets the client detect it. |
| `spec.retention` | `object` | no | retention is how many snapshots to keep under the destination prefix. Default: `{"keepLast":24}`. |
| `spec.retention.keepLast` | `integer` | no | keepLast keeps the newest N snapshots and deletes older ones after a successful upload. Default: `24`. |
| `spec.schedule` | `string` | no | schedule is a standard five-field cron expression, evaluated in UTC. Default: `"0 * * * *"`. |
| `spec.suspend` | `boolean` | no | suspend stops new snapshots; existing ones are kept and not pruned. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of TalosEtcdBackup |
| `status.conditions` | `[]object` | no | conditions: Ready. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.consecutiveFailures` | `integer` | no | consecutiveFailures counts failed attempts since the last success. |
| `status.lastAttemptTime` | `string (date-time)` | no | lastAttemptTime is when the newest attempt started, successful or not. |
| `status.lastManualBackup` | `string (date-time)` | no | lastManualBackup is the time of the newest backup-now request (BackupNowAnnotation) the controller acted on; a request is acted on once, whether the snapshot succeeded or not. |
| `status.lastSnapshot` | `object` | no | lastSnapshot is the newest snapshot this controller stored. |
| `status.lastSnapshot.key` | `string` | yes | key is the object key within the bucket. |
| `status.lastSnapshot.size` | `integer` | yes | size is the stored (encrypted) size in bytes. |
| `status.lastSnapshot.sourceMachine` | `string` | yes | sourceMachine is the TalosMachine the snapshot was read from. |
| `status.lastSnapshot.time` | `string (date-time)` | yes | time is when the snapshot was taken. |
| `status.lastSuccessTime` | `string (date-time)` | no | lastSuccessTime is when the newest successful attempt finished. |
| `status.snapshots` | `[]object` | no | snapshots lists the snapshots kept in the destination after the last backup and prune, newest first, so the UI and CLI can offer them for a restore without reading the bucket. |
| `status.snapshots[].name` | `string` | yes | name is the object name under the prefix: what an etcd-restore operation's spec.etcdRestore.snapshot says. |
| `status.snapshots[].size` | `integer` | yes | size is the stored (encrypted) size in bytes. |
| `status.snapshots[].time` | `string (date-time)` | yes | time is when the snapshot was taken (encoded in its name). |

