---
title: "MachineReclaim"
description: "MachineReclaim is the Schema for the machinereclaims API"
---

# MachineReclaim

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

MachineReclaim is the Schema for the machinereclaims API

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `MachineReclaim` (list kind `MachineReclaimList`) |
| Resource | `machinereclaims` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of MachineReclaim |
| `spec.address` | `string` | yes | address is the one node to reclaim: an IP or host:port of its Talos API. Only this address is dialled; the endpoints in the talosconfig are ignored. |
| `spec.clusterRef` | `object` | no | clusterRef optionally names the TalosCluster (same namespace) the machine is meant to join after the reset. The controller never writes it or that cluster; the UI API adds the address to the cluster's discovery addresses on the user's behalf when it creates the request. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.confirmClusterName` | `string` | no | confirmClusterName is the foreign cluster's name as typed by the user; only needed with force. |
| `spec.confirmUUID` | `string` | no | confirmUUID is the node's UUID as typed by the user. Empty until then; the webhook rejects a value that differs from status.inspection.uuid. |
| `spec.contextName` | `string` | no | contextName picks the talosconfig context; empty is the file's default. |
| `spec.credentialSecretRef` | `string` | yes | credentialSecretRef names a Secret of type ichor.maesh.dev/foreign-talosconfig in this namespace. It is deleted when the request ends or expires. |
| `spec.force` | `boolean` | no | force allows a control plane whose removal breaks the foreign etcd quorum or is the last of its cluster. It also needs confirmClusterName. |
| `spec.forceUngraceful` | `boolean` | no | forceUngraceful sends a non-graceful reset, for a foreign cluster that is dead. |
| `spec.ttl` | `string` | no | ttl is how long the request and its credential live. Default 15 minutes, at most 60. Default: `"15m"`. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of MachineReclaim |
| `status.audit` | `object` | no | audit records who requested and confirmed. |
| `status.audit.confirmedAt` | `string (date-time)` | no | confirmedAt is when the controller acted on the confirmation. |
| `status.audit.confirmedBy` | `string` | no | confirmedBy is the user that wrote confirmUUID, recorded by the webhook. |
| `status.audit.credentialDeletedAt` | `string (date-time)` | no | credentialDeletedAt is when the controller deleted the Secret. |
| `status.audit.requestedBy` | `string` | no | requestedBy is the Kubernetes user that created the request, recorded by the webhook. |
| `status.audit.resetSentAt` | `string (date-time)` | no | resetSentAt is set before the reset is sent; its presence refuses a second reset. |
| `status.conditions` | `[]object` | no | conditions represent the current state of the request. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.expiresAt` | `string (date-time)` | no | expiresAt is creation time plus ttl; after it the credential is gone and nothing is sent. |
| `status.inspection` | `object` | no | inspection is the last read-only look at the node. |
| `status.inspection.clusterID` | `string` | no |  |
| `status.inspection.clusterName` | `string` | no |  |
| `status.inspection.clusterReachable` | `boolean` | no |  |
| `status.inspection.etcdMembers` | `[]object` | no |  |
| `status.inspection.etcdMembers[].healthy` | `boolean` | yes | healthy is false when the member did not answer its health check. |
| `status.inspection.etcdMembers[].hostname` | `string` | no |  |
| `status.inspection.etcdMembers[].id` | `integer` | yes |  |
| `status.inspection.graceful` | `boolean` | no |  |
| `status.inspection.hostname` | `string` | no |  |
| `status.inspection.needsForce` | `boolean` | no |  |
| `status.inspection.refusals` | `[]string` | no | refusals are why the reset cannot be sent; empty means it can, once confirmed. |
| `status.inspection.role` | `string` | no |  |
| `status.inspection.talosVersion` | `string` | no |  |
| `status.inspection.uuid` | `string` | no |  |
| `status.inspection.volumes` | `[]object` | no |  |
| `status.inspection.volumes[].encrypted` | `boolean` | no | encrypted volumes lose their key with the reset; their data becomes unreadable. |
| `status.inspection.volumes[].location` | `string` | no |  |
| `status.inspection.volumes[].name` | `string` | yes |  |
| `status.inspection.volumes[].sizeBytes` | `integer` | no |  |
| `status.inspection.warnings` | `[]string` | no |  |
| `status.phase` | `string` | no | phase is where the request is. Allowed values: `Inspecting`, `AwaitingConfirmation`, `Resetting`, `WaitingForMaintenance`, `Done`, `Failed`, `Expired`. |
| `status.result` | `string` | no | result is the new maintenance address, or the scan hint, or why the request failed. |

