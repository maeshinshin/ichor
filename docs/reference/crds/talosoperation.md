---
title: "TalosOperation"
description: "TalosOperation is the Schema for the talosoperations API."
---

# TalosOperation

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosOperation is the Schema for the talosoperations API. It is a record of an imperative action in progress, not a declarative desired state.

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosOperation` (list kind `TalosOperationList`) |
| Resource | `talosoperations` |
| Short names | `top` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of TalosOperation |
| `spec.caRotation` | `object` | no | caRotation chooses which CAs a ca-rotation-kind operation replaces. Absent means the Talos API CA and the Kubernetes API CA, as before. |
| `spec.caRotation.targets` | `[]string` | no | targets lists the CAs to replace: talos, kubernetes, etcd, kubernetes-aggregator. Empty means talos and kubernetes. The set is fixed when the rotation starts; the new CAs saved in the bundle Secret are what a restarted controller follows. Allowed values: `talos`, `kubernetes`, `etcd`, `kubernetes-aggregator`. |
| `spec.clusterRef` | `object` | yes | clusterRef names the TalosCluster this operation acts on. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.cni` | `object` | no | cni says which cni-kind operation this is. Absent means the kube-proxy switch; action upgrade is a Cilium chart upgrade. |
| `spec.cni.action` | `string` | yes | action is upgrade: run the Cilium Helm Job to move the release to the chart this Ichor embeds. Allowed values: `upgrade`. |
| `spec.cni.chartVersion` | `string` | yes | chartVersion is the Cilium chart version the operation upgrades to. The executor refuses to run when the controller embeds another version (it was upgraded after the operation was created). |
| `spec.cni.imageVersion` **(deprecated)** | `string` | no | imageVersion is deprecated and unused. An earlier Ichor made the upgrade a Cilium patch with it. Cilium now moves only to the chart the running Ichor embeds (amendment 2026-11-04 (3)): the executor refuses an operation that sets it. Kept so the schema does not lose a field an installed build had. |
| `spec.etcdRestore` | `object` | no | etcdRestore is the snapshot of an etcd-restore-kind operation. |
| `spec.etcdRestore.backupRef` | `object` | yes | backupRef names the TalosEtcdBackup (same namespace) whose storage holds the snapshot. Its credentials, prefix and the cluster's secrets bundle (the decryption key) are what the controller reads the snapshot with. |
| `spec.etcdRestore.backupRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.etcdRestore.confirm` | `string` | yes | confirm must equal the cluster's name: the restore rewinds the whole cluster, so the name is typed. |
| `spec.etcdRestore.reason` **(deprecated)** | `string` | no | reason is deprecated: unused, kept so the schema never drops a field. It was the override reason the restore was started under; it is no longer required, asked for or read. |
| `spec.etcdRestore.snapshot` | `string` | yes | snapshot is the object name of the snapshot under the backup's prefix, as listed in the backup's status.snapshots. |
| `spec.keyRotation` | `object` | no | keyRotation is the key-rotation kind's request: which keys, and how long the old service-account key is kept. |
| `spec.keyRotation.ignoreLegacyTokens` | `boolean` | no | ignoreLegacyTokens lets the rotation drop the old service-account key although Secrets of type kubernetes.io/service-account-token that it signed still exist. Those tokens stop working when the key is dropped. By default the operation waits until none is left. Default: `false`. |
| `spec.keyRotation.targets` | `[]string` | yes | targets names the keys to replace: serviceAccount (the key that signs ServiceAccount tokens), secretbox (the key Secrets are encrypted with in etcd), or both. Allowed values: `serviceAccount`, `secretbox`. |
| `spec.keyRotation.tokenSettle` | `string` | no | tokenSettle is how long the old service-account key is kept after the new one signs, so that the projected tokens of running pods are re-issued under the new one. The default is 90 minutes and the shortest accepted is 75 minutes (a shorter one makes pods that still hold an old token fail until the kubelet refreshes it). Raise it when workloads request projected tokens that live longer than an hour. Ignored without the serviceAccount target. |
| `spec.kind` | `string` | yes | kind names which upgrade this is: talos (spec.talosVersion/schematic), kubernetes (spec.kubernetesVersion), or ichor (the controller/CRDs/UI install), cni (a spec.cni.kubeProxy switch), shutdown (a planned stop of the whole cluster), or ca-rotation (the Talos API and Kubernetes API CAs). This CRD and its controller only store and report it -- each kind's own execution is a separate unit's work. shutdown and ca-rotation are the exceptions: each runs its own state machine. config applies the pending reboot-requiring configuration changes, one machine at a time with a reboot each. etcd-restore rewinds the cluster to a stored etcd snapshot. key-rotation replaces the service-account signing key and the secretbox key. Allowed values: `talos`, `kubernetes`, `ichor`, `cni`, `shutdown`, `ca-rotation`, `config`, `node`, `etcd-restore`, `key-rotation`. |
| `spec.maxUnavailable` | `integer` | no | maxUnavailable caps how many worker machines this operation advances at once. Control planes are never parallel: this operation advances exactly one at a time, and only once every control plane has finished, regardless of this value. Default: `1`. |
| `spec.node` | `object` | no | node is the action of a node-kind operation: which machine and what to do to it. |
| `spec.node.action` | `string` | yes | action is what to do: reboot, shutdown, restart-service, etcd-defrag (control planes), etcd-forfeit-leadership (the control plane that leads etcd), or reset (the removal flow with the node wiped). etcd member removal is not an action: it is part of removal. Allowed values: `reboot`, `shutdown`, `restart-service`, `etcd-defrag`, `etcd-forfeit-leadership`, `reset`. |
| `spec.node.allowLeader` | `boolean` | no | allowLeader lets etcd-defrag run on the member that currently leads a multi-member etcd. By default it is refused there. Default: `false`. |
| `spec.node.breakGlassReason` **(deprecated)** | `string` | no | breakGlassReason is the earlier name of overrideReason, kept because CRD fields are append-only and operations written by older builds carry it. Deprecated: use override. Ichor's own CLI and UI never set it; its text is not read, but a value still counts, and is gated by the same verb, as an override. |
| `spec.node.confirm` | `string` | no | confirm must equal the machine's name for reset, the typed confirmation of an irreversible action. |
| `spec.node.machineRef` | `object` | yes | machineRef names the TalosMachine the action runs on. |
| `spec.node.machineRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.node.override` | `boolean` | no | override asks to override a safety check: the last-worker guard for reboot and shutdown, or the reset's own gate. It is only honoured when the creator holds the override verb on the machine (the override webhook checks), and it goes into the audit record and an Event. |
| `spec.node.overrideReason` **(deprecated)** | `string` | no | overrideReason is deprecated: unused, kept so the schema never drops a field. It was the optional reason for an override; no client sets it and nothing reads it. A value written by an older build still counts as an override (see Overrides). |
| `spec.node.powerOff` | `boolean` | no | powerOff makes the node power off after the reset wipes it instead of rebooting into maintenance mode. Only with the reset action; unset keeps the reboot, which is what a reset did before the field existed. The UI and `ichor delete node` set it unless asked to reboot. |
| `spec.node.service` | `string` | no | service is the Talos service restart-service restarts. Only kubelet, containerd and cri are allowed; etcd, apid, trustd and machined never are. Allowed values: `kubelet`, `containerd`, `cri`. |
| `spec.node.wipeMode` | `string` | no | wipeMode makes a reset wipe more than the STATE and EPHEMERAL partitions: system-disk erases the whole system disk (boot and EFI included, so the node boots a boot image again instead of its installed UKI, which is what installing it afresh from a SecureBoot ISO needs), all erases the same system disk and never a user disk. Only with the reset action; unset keeps the STATE and EPHEMERAL wipe. Allowed values: `system-disk`, `all`. |
| `spec.paused` | `boolean` | no | paused stops the operation from advancing any further machine. It does not undo a machine already advanced, and it does not release the cluster lock -- the operation is not finished, only halted. It has no effect on an etcd restore that has started: the restore takes the Kubernetes API away and the controller drives it to the end, or to a failed step that pauses it, whatever this field says. Default: `false`. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of TalosOperation |
| `status.caRotation` | `object` | no | caRotation is the ca-rotation kind's state machine: which stage every node is being taken to, and which CA is accepted and which issues in it. Public fingerprints only. |
| `status.caRotation.apiServerInstances` | `map[string]string` | no | apiServerInstances is, for each control plane the current stage was applied to in a rotation that restarts kube-apiserver (the Kubernetes or the aggregator CA), the kube-apiserver container it ran in just before the apply. The stage is verified once that control plane runs another one and answers /readyz, so the operation is not Completed while a restart is still to come. Emptied when the stage changes. Not a secret. |
| `status.caRotation.appliedAt` | `string (date-time)` | no | appliedAt is when the current stage was last applied to a machine; a kube-apiserver whose container did not change within a minute of it is taken as not restarted by this stage. |
| `status.caRotation.certificates` | `[]object` | no | certificates lists the two rotated CAs and, for the current stage, which fingerprint issues and which are accepted. |
| `status.caRotation.certificates[].accepted` | `[]string` | no | accepted lists the CAs, besides the issuing one, that nodes accept in the current stage. |
| `status.caRotation.certificates[].issuing` | `string` | no | issuing is the CA that issues certificates in the current stage. |
| `status.caRotation.certificates[].name` | `string` | yes | name is talos (the Talos API CA), kubernetes (the Kubernetes API CA), etcd (the etcd CA) or kubernetes-aggregator (the aggregator, or front-proxy, CA). |
| `status.caRotation.certificates[].next` | `string` | yes | next is the new CA, whose key is in the bundle Secret's rotation.yaml until the rotation is finalized. |
| `status.caRotation.certificates[].previous` | `string` | yes | previous is the CA the rotation started from. |
| `status.caRotation.etcdRebootBootID` | `string` | no | etcdRebootBootID is the boot ID etcdRebooting had when its reboot was requested; the reboot has happened once the node reports another one. Not a secret. |
| `status.caRotation.etcdRebooting` | `string` | no | etcdRebooting is the control plane whose reboot was requested and is not yet over; empty between two reboots. |
| `status.caRotation.etcdRestarted` | `[]string` | no | etcdRestarted names the control planes that were rebooted, and are healthy again, in the current stage, in the order they were rebooted (Talos does not restart etcd on a configuration change and its etcd service has no restart API, so a member loads the stage's CA with a node reboot). Emptied when the stage changes. |
| `status.caRotation.etcdRestartedAt` | `string (date-time)` | no | etcdRestartedAt is when the reboot of the control plane in etcdRebooting, or the last one, was requested. |
| `status.caRotation.stage` | `string` | no | stage is the stage every node is being taken to; see the CARotationStage* constants. Empty until the new CAs have been generated and saved. Allowed values: `Accepting`, `AwaitingCredentials`, `Issuing`, `Dropping`, `Finalizing`, `Done`. |
| `status.conditions` | `[]object` | no | conditions represent the current state of the TalosOperation resource. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.etcdRestore` | `object` | no | etcdRestore is the etcd-restore kind's state machine. |
| `status.etcdRestore.message` | `string` | no | message is a plain-English line about what the operation is doing or waiting for. |
| `status.etcdRestore.reconcileRequest` | `string` | no | reconcileRequest is the token every TalosMachine was asked to acknowledge in the last step. |
| `status.etcdRestore.requestedAt` | `string (date-time)` | no | requestedAt is when the destructive part started; waits are bounded from it. |
| `status.etcdRestore.snapshotTime` | `string (date-time)` | no | snapshotTime is when the snapshot being restored was taken. |
| `status.etcdRestore.step` | `string` | no | step is Wiping, Recovering, Rejoining or Reconciling; empty until the snapshot was verified and the marker written. Allowed values: `Wiping`, `Recovering`, `Rejoining`, `Reconciling`. |
| `status.etcdRestore.target` | `string` | no | target is the control plane the snapshot is recovered on. |
| `status.failureReason` | `string` | no | failureReason explains why phase is Paused when the cause is a machine's own failed step, as opposed to spec.paused. |
| `status.finishedAt` | `string (date-time)` | no | finishedAt is when the operation reached Completed. |
| `status.fromVersion` **(deprecated)** | `string` | no | fromVersion is deprecated and unused: Ichor no longer writes or reads it. The from-version now lives in the ichor.maesh.dev/from-version annotation (OperationFromVersionAnnotation). The field stays so the CRD never drops a field an installed version has. |
| `status.keyRotation` | `object` | no | keyRotation is the key-rotation kind's state machine: which stage every control plane is being taken to, which key signs or encrypts in it, and the progress of the re-encryption. Public identifiers only. |
| `status.keyRotation.keys` | `[]object` | no | keys lists the rotated keys and, for the current stage, which one signs or encrypts and which are accepted. Public identifiers only; no key material. |
| `status.keyRotation.keys[].accepted` | `[]string` | no | accepted lists the keys, besides the active one, that verify or decrypt in the current stage. |
| `status.keyRotation.keys[].active` | `string` | no | active is the key that signs (serviceAccount) or encrypts (secretbox) in the current stage. |
| `status.keyRotation.keys[].name` | `string` | yes | name is serviceAccount or secretbox. |
| `status.keyRotation.keys[].next` | `string` | yes | next is the new key, held in the bundle Secret's keyrotation.yaml until the rotation is finalized. |
| `status.keyRotation.keys[].previous` | `string` | yes | previous is the key the rotation started from. |
| `status.keyRotation.reencryption` | `object` | no | reencryption is the progress of the Reencrypting stage. |
| `status.keyRotation.reencryption.rewritten` | `integer` | no | rewritten is how many Secrets the last complete sweep stored again under the new key. The stage ends after a sweep that rewrote none. |
| `status.keyRotation.reencryption.scanned` | `integer` | no | scanned is how many Secrets the last complete sweep looked at. |
| `status.keyRotation.reencryption.sweeps` | `integer` | no | sweeps is how many complete passes over every Secret have run. |
| `status.keyRotation.restart` | `object` | no | restart is the control plane whose kube-apiserver the current stage restarted last, and the container it ran before. The stage waits for another container and /readyz before it visits the next control plane. |
| `status.keyRotation.restart.instance` | `string` | no | instance is the kube-apiserver container that ran before the restart. |
| `status.keyRotation.restart.machine` | `string` | yes | machine is the TalosMachine whose control plane was told to restart its kube-apiserver. |
| `status.keyRotation.settlingSince` | `string (date-time)` | no | settlingSince is when the SettlingTokens stage started waiting for the projected tokens signed by the old service-account key to be re-issued. The wait ends spec.keyRotation.tokenSettle after it. |
| `status.keyRotation.stage` | `string` | no | stage is the stage every control plane is being taken to; see the KeyRotationStage* constants. Empty until the new keys have been generated and saved. Allowed values: `Accepting`, `Issuing`, `Reencrypting`, `SettlingTokens`, `Dropping`, `Finalizing`, `Done`. |
| `status.kubernetes` | `object` | no | kubernetes is the kubernetes kind's state machine: which of the three stages the machines are being taken through. |
| `status.kubernetes.stage` | `string` | no | stage is APIServers (kube-apiserver on every control plane), ControlPlaneComponents (controller-manager and scheduler on every control plane; the kubelets and kube-proxy still on the old version), KubeProxy (kube-proxy and the bootstrap manifests) or Kubelets (every kubelet, control planes first, then workers). Allowed values: `APIServers`, `ControlPlaneComponents`, `KubeProxy`, `Kubelets`. |
| `status.lastRetry` | `string (date-time)` | no | lastRetry is the time of the retry request (the ichor.maesh.dev/retry annotation) the controller last acted on: it cleared failureReason and ran the failed step again. |
| `status.machines` | `[]object` | no | machines records every machine this operation has ever selected, and how far each got. A name's absence means "never selected" -- held, not yet its turn, or the operation finished before it was reached -- not a fourth phase value. |
| `status.machines[].bootID` | `string` | no | bootID is the machine's boot ID recorded just before it was asked to stop (shutdown kind only; empty for the other kinds, and for a machine that was already unreachable). A different boot ID later is how the operation knows the machine has been powered back on. |
| `status.machines[].finishedAt` | `string (date-time)` | no | finishedAt is when this machine's step was reported done. |
| `status.machines[].name` | `string` | yes | name is the TalosMachine's name. |
| `status.machines[].phase` | `string` | yes | phase is this machine's own progress: InProgress (advance was called, not yet done) or Completed (advance reported done). A machine never appears here until it has been selected at least once; one that is held, or simply not yet reached, is absent, not listed as some third phase. Allowed values: `InProgress`, `Completed`. |
| `status.machines[].startedAt` | `string (date-time)` | no | startedAt is when the operation first selected this machine. |
| `status.message` | `string` | no | message is what an InProgress operation is doing or waiting for right now, as the controller last saw it ("waiting for the ichor-ui Deployment to finish rolling out (1 of 2 updated replicas are available)"). It is replaced when it changes and cleared in every other phase; a pause says why in failureReason or the Paused condition, a finished operation in its Events. |
| `status.node` | `object` | no | node is the node kind's state machine. |
| `status.node.machineUID` | `string` | no | machineUID is the UID of the TalosMachine a reset started on. The wait for the removal flow ends when that object is gone or replaced by one with another UID: discovery may recreate a pending machine with the same name within seconds, and that one is never the operation's to delete. |
| `status.node.message` | `string` | no | message is a plain-English line about what the operation is doing or waiting for. |
| `status.node.requestedAt` | `string (date-time)` | no | requestedAt is when the action was sent; the wait for its effect is bounded from it. |
| `status.node.step` | `string` | no | step is empty until the action was sent, then Requested (waiting for its effect) or Removing (a reset waiting for the removal flow). Allowed values: `Requested`, `Removing`. |
| `status.phase` | `string` | no | phase is this operation's own state-machine phase (see the TalosOperationPhase* constants). The whole state machine lives here, not in the controller's memory, so a restart resumes from exactly this value and the machines list below, not from scratch (docs/design/architecture.md, "Upgrades": "the status-held state machine"). Allowed values: `Pending`, `InProgress`, `Paused`, `ShutDown`, `Completed`. |
| `status.toVersion` | `string` | no | toVersion is the Ichor version an ichor operation upgrades to: the controller copies IchorInstallation.spec.version here once, when the upgrade starts, and never rewrites it. It is empty on other kinds and on operations that started before it existed. |

