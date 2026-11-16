---
sidebar_order: 3
---

# Pods and the API server

## Pod Security and audit

```yaml
spec:
  security:
    podSecurity: {enforce: restricted, exemptNamespaces: [rook-ceph]}
    audit: {policy: rbacChanges}
```

Both need `versionContract` v1.14 or later (they are Talos's multi-document
`KubeAdmissionControlConfig` and `KubeAuditPolicyConfig`); on an older contract
Ichor refuses the option instead of raising the contract. Both restart the
kube-apiserver on each control plane; nodes do not reboot. The restart is not
applied on its own: the control plane shows
`Ready=False APIServerRestartRequired` until you run
`ichor rollout config --cluster NAME --follow` (or start the rollout on the
cluster page), which goes one control plane at a time. The condition message
names that command. With one control plane the API is down for about a
minute, and the whole operation takes a few minutes.

`podSecurity` changes only the levels you set (`enforce`, `audit`, `warn`; Talos
defaults are baseline, restricted, restricted) and adds `exemptNamespaces` to
`kube-system`, which stays exempt. Existing pods are not evicted, but new and
updated pods that break `enforce` are rejected. Namespaces that run privileged
workloads (storage, CNI add-ons) need an exemption.

`audit.policy` presets. In none of them is anything about Secrets,
ConfigMaps, service account tokens or token reviews logged above Metadata.

| Preset | Records |
|---|---|
| `metadata` | Who did what, without bodies, for every request (Talos's default policy) |
| `rbacChanges` | `metadata`, plus request and response of changes to roles and bindings |
| `writes` | `metadata`, plus the request body of every change |

Ichor does not change where Talos writes the audit log or ship it anywhere.

## Talos API access from Kubernetes pods

> [!WARNING]
> `machine.features.kubernetesTalosAPIAccess` in a `Config` patch with `os:admin` in `allowedRoles` hands a Talos administrator to every pod in the allowed namespaces.

That is your cluster's choice, so the `TalosConfigPatch` webhook only warns (with extra wording for `os:admin`) and admits the patch. Talos allows the feature on control planes only, so Ichor renders it on control planes and leaves it out of workers, whatever layer the patch is on. The `TalosConfigPatch` webhook refuses it unless the user holds the `override` verb on that patch. Any enabled `kubernetesTalosAPIAccess` is admitted with a warning naming the roles and namespaces. An update that does not touch the documents is not re-checked, so a patch stored earlier stays editable.
