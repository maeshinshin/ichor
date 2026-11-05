---
title: "IchorInstallation"
description: "IchorInstallation is the Schema for the ichorinstallations API: Ichor's own version, a cluster-scoped singleton."
---

# IchorInstallation

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

IchorInstallation is the Schema for the ichorinstallations API: Ichor's own version, a cluster-scoped singleton.

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `IchorInstallation` (list kind `IchorInstallationList`) |
| Resource | `ichorinstallations` |
| Scope | Cluster |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of IchorInstallation |
| `spec.autoPatch` | `object` | no | autoPatch lets the controller apply patch releases of the running minor by itself. Off unless enabled here or by the chart. |
| `spec.autoPatch.acknowledgedHalt` | `string` | no | acknowledgedHalt is how a person clears a halt: the name of the operation (namespace/name) that halted automatic patching, as status.autoPatch.haltedOperation shows it. The controller never edits spec, so it reads this and resumes when it equals the halted operation; a later halt names another operation. |
| `spec.autoPatch.components` | `[]string` | no | components are the components whose patches are applied automatically: talos, kubernetes, cilium. Unset or empty means all three. cilium is accepted and does nothing: Cilium moves only to the chart the running Ichor embeds, which a person starts. Allowed values: `talos`, `kubernetes`, `cilium`. |
| `spec.autoPatch.enabled` | `boolean` | no | enabled turns automatic patching on or off. Unset falls back to the controller flag, which the chart sets from autoPatch.enabled (default false). |
| `spec.autoPatch.window` | `string` | no | window is a daily UTC window "HH:MM-HH:MM" in which an automatic upgrade may start; it may wrap midnight (22:00-02:00). Unset means any time. |
| `spec.defaults` | `object` | no | defaults holds defaults for objects created later; an existing object is never touched. |
| `spec.defaults.driftPolicy` | `string` | no | driftPolicy is the default for a new TalosCluster's spec.driftPolicy. It takes the values of that field (warnOnly, autoCorrect) "enforce" is autoCorrect. Allowed values: `warnOnly`, `autoCorrect`. |
| `spec.deployment` | `object` | no | deployment records who deploys this Ichor. |
| `spec.deployment.manager` | `string` | no | manager is installer (the installer Job, `ichor init`) or helm. Omitted means installer. Allowed values: `installer`, `helm`. |
| `spec.installerDigest` | `string` | no | installerDigest pins the installer image to a digest ("sha256:" and 64 hex digits). Unset, the controller resolves the tag \<version> in installerRepository itself when it creates the Job and records the digest in status.installerImage. Set, it never resolves the tag, so the registry is only asked for the signature (none when installerSignature says skip). Change it together with version. |
| `spec.installerRepository` | `string` | no | installerRepository is the image repository of the installer image; the Job runs \<installerRepository>@\<digest>, where the digest is installerDigest or the one \<version> resolves to in that repository when the Job is created. Point it at a mirror or private registry if you need to; a mirror has to carry the image's signature too (the OCI referrers), or installerSignature has to say skip. Default: `"ghcr.io/maeshinshin/ichor-installer"`. |
| `spec.installerSignature` | `object` | no | installerSignature says how the signature of the installer image is checked before the Job runs. Unset means required. |
| `spec.installerSignature.mode` | `string` | yes | mode is required (the default) or skip. Allowed values: `required`, `skip`. Default: `"required"`. |
| `spec.installerSignature.reason` **(deprecated)** | `string` | no | reason is deprecated: unused, kept so the schema never drops a field. skip no longer needs one and nothing reads it. |
| `spec.reclaim` **(deprecated)** | `object` | no | reclaim is deprecated and ignored: "Reset and add" has no installation switch; RBAC alone (the reclaim verb on machinereclaims) decides who may use it. The field stays so that an installed object keeps validating. The admission webhook warns when it is set. |
| `spec.reclaim.enabled` **(deprecated)** | `boolean` | no | enabled is deprecated and ignored. |
| `spec.updateCheck` | `object` | no | updateCheck holds the switch for the check of newer Ichor releases. |
| `spec.updateCheck.enabled` | `boolean` | no | enabled turns the check on or off. Unset falls back to the controller flag, which the chart sets from updateCheck.enabled (default true). Off stops the registry request and removes the controller's egress rule for it. |
| `spec.users` | `object` | no | users holds the sign-in lifetimes. |
| `spec.users.refreshLifetime` | `string` | no | refreshLifetime is the lifetime of a refresh token, 1h to 720h. Unset falls back to the flag. |
| `spec.users.tokenTTL` | `string` | no | tokenTTL is the lifetime of an issued token, 1m to 1h. Unset falls back to the flag. |
| `spec.version` | `string` | yes | version is the Ichor version the installation should run, as a semantic version with a leading v (v0.4.0). The installer Job runs the image for this version, which carries that version's own manifests. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of IchorInstallation |
| `status.autoPatch` | `object` | no | autoPatch is what the automatic patcher last did and why it stopped. |
| `status.autoPatch.haltedOperation` | `string` | no | haltedOperation is the automatic operation (namespace/name) that failed or paused and stopped automatic patching; empty when it is not halted. |
| `status.autoPatch.lastOperation` | `string` | no | lastOperation is the operation (namespace/name) the patcher started last. |
| `status.autoPatch.lastStartedAt` | `string (date-time)` | no | lastStartedAt is when it started. |
| `status.conditions` | `[]object` | no | conditions represent the current state of the IchorInstallation resource. UpToDate is True when installedVersion equals spec.version. An ExternalSuggested condition from an older build is removed. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.effective` | `object` | no | effective shows each runtime setting with the value in force and where it came from. Written by the controller. |
| `status.effective.autoPatch` | `object` | no | autoPatch is "true" or "false". |
| `status.effective.autoPatch.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.autoPatch.value` | `string` | yes | value is the setting as a string. |
| `status.effective.autoPatchComponents` | `object` | no | autoPatchComponents is the comma-separated list of components in force. |
| `status.effective.autoPatchComponents.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.autoPatchComponents.value` | `string` | yes | value is the setting as a string. |
| `status.effective.autoPatchWindow` | `object` | no | autoPatchWindow is the daily UTC window in force; empty means any time. |
| `status.effective.autoPatchWindow.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.autoPatchWindow.value` | `string` | yes | value is the setting as a string. |
| `status.effective.driftPolicy` | `object` | no | IchorEffectiveValue is one setting in force. |
| `status.effective.driftPolicy.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.driftPolicy.value` | `string` | yes | value is the setting as a string. |
| `status.effective.manager` | `object` | no | IchorEffectiveValue is one setting in force. |
| `status.effective.manager.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.manager.value` | `string` | yes | value is the setting as a string. |
| `status.effective.reclaimEnabled` **(deprecated)** | `object` | no | reclaimEnabled is deprecated: the controller no longer reports it. |
| `status.effective.reclaimEnabled.source` **(deprecated)** | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.reclaimEnabled.value` **(deprecated)** | `string` | yes | value is the setting as a string. |
| `status.effective.refreshLifetime` | `object` | no | IchorEffectiveValue is one setting in force. |
| `status.effective.refreshLifetime.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.refreshLifetime.value` | `string` | yes | value is the setting as a string. |
| `status.effective.tokenTTL` | `object` | no | IchorEffectiveValue is one setting in force. |
| `status.effective.tokenTTL.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.tokenTTL.value` | `string` | yes | value is the setting as a string. |
| `status.effective.updateCheck` | `object` | no | IchorEffectiveValue is one setting in force. |
| `status.effective.updateCheck.source` | `string` | yes | source is spec when the field is set, deployment when the flag or chart value applies. Allowed values: `spec`, `deployment`. |
| `status.effective.updateCheck.value` | `string` | yes | value is the setting as a string. |
| `status.installedVersion` | `string` | no | installedVersion is the version of the running controller, reported by the controller itself: the single source of "what is installed", which is also what the CLI compares itself against. |
| `status.installerImage` | `object` | no | installerImage is the installer image the last Ichor upgrade ran, by digest, and whether its signature was checked. |
| `status.installerImage.digest` | `string` | no | digest is the image digest the Job runs, "sha256:...". |
| `status.installerImage.reason` **(deprecated)** | `string` | no | reason is deprecated: unused, kept so the schema never drops a field. It is no longer written. |
| `status.installerImage.repository` | `string` | no | repository is the image repository the Job pulled from. |
| `status.installerImage.resolvedAt` | `string (date-time)` | no | resolvedAt is when the controller resolved and checked the image. |
| `status.installerImage.signer` | `string` | no | signer is the identity the signature matched (the certificate subject pattern); empty when Skipped. |
| `status.installerImage.verification` | `string` | no | verification is Verified when a cosign signature of the release workflow for version matched the digest, Skipped when spec.installerSignature said skip. Allowed values: `Verified`, `Skipped`. |
| `status.installerImage.version` | `string` | no | version is the Ichor version the image was resolved for. |
| `status.latestRelease` | `object` | no | latestRelease is the newest final release the controller found in the chart repository. Unset until the first check, and cleared while the check is off. |
| `status.latestRelease.checkedAt` | `string (date-time)` | no | checkedAt is when the last check finished, successful or not. |
| `status.latestRelease.error` | `string` | no | error is the failure of the last check; empty after a successful one. |
| `status.latestRelease.version` | `string` | no | version is the highest final release found, with a leading v. A failed check keeps the value of the last good one. |
| `status.upgradeTargets` | `object` | no | upgradeTargets are the Talos and Kubernetes releases the controller found in their image repositories. Unset until the first check, and cleared while the check is off. |
| `status.upgradeTargets.checkedAt` | `string (date-time)` | no | checkedAt is when the last listing finished, successful or not. |
| `status.upgradeTargets.cilium` **(deprecated)** | `[]string` | no | cilium is deprecated and unused. It held the Cilium patches whose images verified; Cilium now moves only to the chart the running Ichor embeds (amendment 2026-11-04 (3)), so the controller no longer writes it and clears what an earlier controller wrote. Kept so the schema does not lose a field an installed build had. |
| `status.upgradeTargets.ciliumError` **(deprecated)** | `string` | no | ciliumError is deprecated and unused, like cilium: the controller no longer lists Cilium releases. |
| `status.upgradeTargets.error` | `string` | no | error is the failure of the last listing; a list that failed keeps its last good value. |
| `status.upgradeTargets.kubernetes` | `[]string` | no | kubernetes are the final Kubernetes releases, newest first, with a leading v. |
| `status.upgradeTargets.kubernetesError` | `string` | no | kubernetesError is why the last listing of the Kubernetes releases failed; empty when it succeeded. |
| `status.upgradeTargets.talos` | `[]string` | no | talos are the final Talos releases, newest first, with a leading v. |
| `status.upgradeTargets.talosError` | `string` | no | talosError is why the last listing of the Talos releases failed; empty when it succeeded. The other lists are unaffected. |

