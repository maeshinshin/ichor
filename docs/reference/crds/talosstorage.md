---
title: "TalosStorage"
description: "TalosStorage is the Schema for the talosstorages API"
---

# TalosStorage

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosStorage is the Schema for the talosstorages API

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosStorage` (list kind `TalosStorageList`) |
| Resource | `talosstorages` |
| Short names | `tsto` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of TalosStorage |
| `spec.clusterRef` | `object` | yes | clusterRef names the TalosCluster this storage configuration applies to. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.diskPreset` | `string` | yes | diskPreset names which storage system this configuration prepares Talos for. Each preset fixes the required system extensions, the UserVolumeConfig shape, and the kubelet mount that preset needs; see pkg/storage. Allowed values: `longhorn-v1`. |
| `spec.diskSelector` | `string` | yes | diskSelector is a Talos disk-selector CEL expression (the same language provisioning.diskSelector.match uses in a VolumeConfig/UserVolumeConfig/RawVolumeConfig document) choosing which disk on each selected node is handed to the storage system. |
| `spec.evacuationTimeout` | `string` | no | evacuationTimeout bounds how long the node-operation hook (layer 4) waits for replicas to evacuate a node before pausing the operation rather than proceeding. Unset means the hook's own default. |
| `spec.nodes` | `object` | yes | nodes selects which nodes get storage prepared, by their Kubernetes Node labels. |
| `spec.nodes.matchExpressions` | `[]object` | no | matchExpressions is a list of label selector requirements. The requirements are ANDed. |
| `spec.nodes.matchExpressions[].key` | `string` | yes | key is the label key that the selector applies to. |
| `spec.nodes.matchExpressions[].operator` | `string` | yes | operator represents a key's relationship to a set of values. Valid operators are In, NotIn, Exists and DoesNotExist. |
| `spec.nodes.matchExpressions[].values` | `[]string` | no | values is an array of string values. If the operator is In or NotIn, the values array must be non-empty. If the operator is Exists or DoesNotExist, the values array must be empty. This array is replaced during a strategic merge patch. |
| `spec.nodes.matchLabels` | `map[string]string` | no | matchLabels is a map of {key,value} pairs. A single {key,value} in the matchLabels map is equivalent to an element of matchExpressions, whose key field is "key", the operator is "In", and the values array contains only "value". The requirements are ANDed. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of TalosStorage |
| `status.conditions` | `[]object` | no | conditions represent the current state of the TalosStorage resource, kstatus-compatible. Ready reports whether the Talos side (layer 1) is prepared, so Argo CD's sync wave can hold the storage system's Application until this is true (design section "Storage"). |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.observedGeneration` | `integer` | no | observedGeneration is the most recent generation the controller has reconciled. |

