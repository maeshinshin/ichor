---
title: "TalosCredentialRequest"
description: "TalosCredentialRequest is a short-lived request for one certificate."
---

# TalosCredentialRequest

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosCredentialRequest is a short-lived request for one certificate. The controller deletes it a couple of minutes after it was created (ADR 0108).

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosCredentialRequest` (list kind `TalosCredentialRequestList`) |
| Resource | `taloscredentialrequests` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec is the request. |
| `spec.clusterRef` | `object` | yes | clusterRef names the TalosCluster, in this namespace, whose CAs sign the certificate. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.csr` | `string` | no | csr is a PEM PKCS#10 certificate request. Only its public key is used (its signature proves the requester holds the key); the subject and any SANs are ignored and replaced. |
| `spec.machineRef` | `object` | no | machineRef names the TalosMachine a download purpose is for; the requester's permission is checked on that machine. |
| `spec.machineRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.purpose` | `string` | yes | purpose decides the subject, the CA, the Talos role and the maximum lifetime, and which Kubernetes permission the requester needs (checked by the admission webhook). Allowed values: `public`, `node-reader`, `support-bundle`, `etcd-snapshot`, `packet-capture`, `credentials-talos`, `credentials-kubernetes`, `talosconfig-reader`. |
| `spec.talosRole` | `string` | no | talosRole is os:reader or os:admin for purpose credentials-talos; empty means admin. It is ignored by every other purpose. Allowed values: `reader`, `admin`. |
| `spec.ttl` | `string` | no | ttl is the lifetime asked for. Empty means the purpose's default (10 minutes for a dial certificate, one year for the operator credentials); the controller refuses more than the purpose's cap. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status is the answer. |
| `status.certificate` | `string` | no | certificate is the signed client certificate (PEM) for the key in spec.csr. |
| `status.message` | `string` | no | message says why a request failed. |
| `status.notAfter` | `string (date-time)` | no | notAfter is when the certificate expires. |
| `status.phase` | `string` | no | phase is Issued or Failed; empty while the controller has not answered. |
| `status.public` | `object` | no | public is set for purpose public. |
| `status.public.clusterID` | `string` | no | clusterID is the Talos cluster ID. |
| `status.public.etcdCA` | `string` | no |  |
| `status.public.kubernetesAggregatorCA` | `string` | no |  |
| `status.public.kubernetesCA` | `string` | no |  |
| `status.public.talosCA` | `string` | no | talosCA, kubernetesCA, etcdCA and kubernetesAggregatorCA are the CA certificates (PEM). |
| `status.trust` | `string` | no | trust is the CA certificate(s) (PEM) the certificate's API must be verified against: the current CA, or the old and the new one during a CA rotation. |

