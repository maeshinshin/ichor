---
title: "IchorUser"
description: "IchorUser is the Schema for the ichorusers API."
---

# IchorUser

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

IchorUser is the Schema for the ichorusers API. Its name is the identity: it becomes the token subject and the Kubernetes user ichor:<name>, and it cannot change (a rename is a new object).

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `IchorUser` (list kind `IchorUserList`) |
| Resource | `ichorusers` |
| Short names | `iusr` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of IchorUser |
| `spec.disabled` | `boolean` | no | disabled refuses every login and refresh for this user without deleting it. |
| `spec.displayName` | `string` | no | displayName is the human-readable name shown in the UI. |
| `spec.email` | `string` | no | email is informational only; nothing is sent to it and it is not an identity. |
| `spec.roles` | `[]string` | no | roles names the groups the user is in: each becomes the Kubernetes group ichor:\<role> (viewer, operator and admin are the built-in ones). |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of IchorUser |
| `status.conditions` | `[]object` | no | conditions: Ready and PasswordSet. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.observedGeneration` | `integer` | no | observedGeneration is the most recent generation the controller has reconciled. |

