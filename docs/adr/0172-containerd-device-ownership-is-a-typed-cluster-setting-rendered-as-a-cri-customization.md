# 0172. containerd's `device_ownership_from_security_context` is a typed cluster setting, rendered as a CRI customization

Status: Accepted · Date: 2026-11-22 · Design section: Typed configuration · Builds on: 0003

Decided: `TalosCluster.spec.containerd.deviceOwnershipFromSecurityContext` (bool, default false) makes Ichor render one `CRICustomizationConfig` document that sets `device_ownership_from_security_context = true` in containerd's CRI runtime plugin; the CLI and the UI both set it by writing that field, and a change restarts containerd without a node reboot.

## Context

containerd's CRI runtime plugin has the option `device_ownership_from_security_context`, which is `false` in the reference configuration of containerd 2.x (docs/cri/config.md, configuration version 3). With it off, containerd leaves a container's device nodes owned by root; with it on, ownership follows the pod's `runAsUser` and `fsGroup`. A pod that runs as a non-root user, such as a KubeVirt virt-launcher or a CDI importer (uid 107), therefore cannot open a `volumeMode: Block` volume while it is off. On a Rook Ceph `ceph-block` cluster this showed as `blockdev: cannot open /dev/cdi-block-volume: Permission denied` in a CDI import.

Talos v1.14 carries containerd customizations in `CRICustomizationConfig`, a named document whose `content` is a TOML fragment merged into the CRI configuration in lexicographical order by name. Applying, updating or removing one restarts CRI and needs no reboot. Ichor classifies that kind as unstructured (`internal/tools/mirrorgen/kinds.go`), so a user cannot reach it through `TalosConfigPatch`, and must not be able to: a TOML string would be a raw escape hatch.

## Decision

1. The field is `spec.containerd.deviceOwnershipFromSecurityContext`, a bool, optional. `spec.containerd` is a new struct, `TalosClusterContainerd`, so a later containerd setting has a place without another top-level field. Unset and false render exactly what they did before: no document.
2. When true, the system layer adds `CRICustomizationConfig` named `ichor-device-ownership` to every node, control plane and worker, with a fixed fragment that sets the key in `[plugins."io.containerd.cri.v1.runtime"]`, the plugin ID of containerd 2.x configuration version 3. The user supplies no TOML; Ichor owns the text. The system layer sits below every patch, so a patch still wins on the same key.
3. It needs `versionContract` v1.14 or later, the first contract with the document. The admission webhook and `Render` both refuse true under an older contract, as they do for `spec.security.podSecurity`.
4. `pkg/fieldmeta` classifies `CRICustomizationConfig` as safe (no reboot), after v1.13: Talos restarts CRI itself. Running containers keep running through a containerd restart, and the new option applies to containers created afterwards, so an existing pod must be recreated to benefit. The machine controller applies it the way it applies registry mirrors, which restart CRI the same way.
5. The CLI sets it with `ichor set containerd-device-ownership true|false`, the UI with a switch on the cluster's page. Both call one ClusterService RPC that writes only this field as the caller, so Kubernetes RBAC on `talosclusters` update decides, as for the discovery edits. Neither writes to a node.
6. The controller does not mutate `spec`; the field is set only by a person through the CLI, the UI or a manifest.

## Consequences

- The CRD gains one object and one bool. The chart's release record grows by that much; `make check-release-record` measures it in the change that adds the field.
- Turning it on restarts containerd on every node at about the same time. Pods keep running, but the CRI is unavailable for a moment, so a pod starting in that window waits.
- Existing pods are not changed. A Block PVC that failed stays failed until its pod is recreated.
- The option is cluster-wide. It lets the pod's security context decide device ownership for every workload, which is what Kubernetes intends for `runAsUser` and `fsGroup`, and is the reason it is opt-in.

## Alternatives considered

- A raw `CRICustomizationConfig` in `TalosConfigPatch`. Rejected: it is a TOML escape hatch, against ADR 0003.
- A field under `spec.security`. Rejected: it is a runtime compatibility option, not hardening, and `spec.security` documents opt-in hardening only.
- Writing `/etc/cri/conf.d/20-customization.part` through `machine.files`. Rejected: it is the deprecated form, and it works on the same contract floor only by taking the reserved name `customization`, which a user patch may already use.
- A per-node or per-pool setting. Rejected: the failure is a cluster property, and a pod can be scheduled on any node.
