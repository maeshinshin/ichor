# Prepare Talos nodes for Rook/Ceph

Ichor prepares the Talos side of a node for Rook/Ceph and nothing else. Rook,
Ceph and the choice of disks stay with you.

![How a node is made ready for Rook. Ichor leaves the disk raw (option A, TalosStorage rook-v1) or carves a raw partition (option B, a TalosConfigPatch). The disk must have no partition or filesystem. You then exempt rook-ceph from Pod Security, name the nodes and devices in the chart values and install the two Rook charts. Rook takes the device, and an Ichor drain waits on Rook's PodDisruptionBudgets.](../images/rook-ceph-node-prep.light.drawio.svg)

*Ichor leaves a raw disk or partition and exempts the namespace; the Rook charts and the device names are yours.*

| Ichor does | Ichor does not |
|---|---|
| Renders `TalosStorage` `rook-v1` as a node that leaves its disks alone ([Storage](../design/architecture/storage.md)) | Install Rook, the operator, the CSI drivers, the `CephCluster`, pools or the StorageClass |
| Renders a labelled raw partition from a `TalosConfigPatch` (option B) | Choose which devices become OSDs |
| Exempts `rook-ceph` from Pod Security | Run `ceph osd out`, wait for `active+clean`, or purge an OSD |
| Gates node drains through Rook's disruption budgets ([Drains](#drains)) | Clean a disk a previous system left behind |

`rook-v1` needs no Talos system extension, so it makes no Image Factory call
and no operation waits for approval. A preset that does need extensions
(`longhorn-v1`) pauses an upgrade that you start with
`ichor approve operation NAME`.

## The disk must be unformatted

Rook (v1.19.6 prerequisites) takes a raw device, a raw partition or an LVM
logical volume, with no filesystem. `lsblk -f` shows a filesystem when `FSTYPE`
is not empty. LVM is needed only for encryption, a `metadata` device or
`osdsPerDevice` above 1; Ichor configures none.

> [!WARNING]
> Do not give Rook a `UserVolumeConfig`. Talos formats a user volume (`xfs` by
> default), and Rook cannot use a formatted disk.

## Option A: leave the disk alone (`rook-v1`)

Label the machines, then declare the storage for them:

```yaml {9,21}
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosMachine
metadata:
  name: worker-1
  namespace: <the TalosCluster's namespace>
spec:
  # ...the rest of the machine
  nodeLabels:
    storage: rook
---
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosStorage
metadata:
  name: rook
  namespace: <the TalosCluster's namespace>
spec:
  clusterRef:
    name: my-cluster
  nodes:
    matchLabels:
      storage: rook
  diskPreset: rook-v1
```

- `spec.nodes` matches the machine's own `spec.nodeLabels`, not the labels on the Kubernetes Node.
- Leave `spec.diskSelector` out: the API refuses it for `rook-v1`.

## Option B: a raw partition

To keep Rook off the whole disk, write a Talos raw volume as a
`TalosConfigPatch` at the `selector` layer:

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosConfigPatch
metadata:
  name: rook-osd-partition
  namespace: <the TalosCluster's namespace>
spec:
  clusterRef:
    name: my-cluster
  layer: selector
  target:
    labelSelector:
      matchLabels:
        storage: rook
  config:
    machine:
      volumes:
        raw:
          osd-data:
            provisioning:
              diskSelector:
                match: "!system_disk"
              maxSize: 100%
```

- The partition label is `r-<volume-name>`; the name is 1 to 34 ASCII letters, digits or `-`.
- Ceph will not create a partition whose label contains `ceph`: `osd-data` works, `ceph-osd-data` (`r-ceph-osd-data`) does not.
- Removing the patch keeps the data. Wiping is separate: `talosctl wipe disk ... --drop-partition`.
- Talos links the partition as `/dev/disk/by-partlabel/r-osd-data`. Whether Rook's device selection accepts that path is not established here; check it against your Rook version.

## Keep the namespace out of Pod Security

Ceph's daemons need privileged access. In the `TalosCluster`:

```yaml
spec:
  security:
    podSecurity:
      exemptNamespaces: [rook-ceph]
```

`kube-system` stays exempt. The change restarts the kube-apiserver on each
control plane and reboots no node; see [Pods and the API server](hardening/pods-and-api.md).

## Data stays under `/var`

Rook's `dataDirHostPath` defaults to `/var/lib/rook`, on Talos's writable
EPHEMERAL partition; Ichor mounts nothing extra. `/var` survives reboots and
upgrades but not a reset, so plan resets with the mons in mind. Rook forbids
`/etc/ceph`, `/rook` and `/var/log/ceph` (and subpaths) as `dataDirHostPath`.

## Name the devices

> [!WARNING]
> The `rook-ceph-cluster` chart defaults to `useAllNodes: true` and
> `useAllDevices: true`, which formats every device and partition it finds,
> data included.

Turn both off and name the nodes and devices in the chart values:

```yaml
cephClusterSpec:
  storage:
    useAllNodes: false
    useAllDevices: false
    nodes:
      - name: worker-1        # must match the kubernetes.io/hostname label
        devices:
          - name: sdb
```

`deviceFilter` and `devicePathFilter` are regular expressions over the same
names; a filter that matches a disk you did not mean is the same mistake.

## Install the charts

Install `rook-ceph`, then `rook-ceph-cluster`, in the `rook-ceph` namespace.
Pin the chart version you read the documentation for (v1.19.6 here):

```sh
helm install --create-namespace -n rook-ceph rook-ceph rook-release/rook-ceph --version v1.19.6
helm install -n rook-ceph rook-ceph-cluster rook-release/rook-ceph-cluster --version v1.19.6 -f values.yaml
```

### Helm replaces lists: restate the StorageClass parameters

Helm merges maps from your values into the chart's defaults but replaces a list
whole. `cephBlockPools`, `cephFileSystems` and `cephObjectStores` are lists, so
an override drops the default entry and its `storageClass.parameters` (the
`csi.storage.k8s.io/*` Secret names). A StorageClass without them provisions
nothing: the claim stays `Pending` with `provided secret is empty`.

Write the parameters in every entry you override:

```yaml
cephBlockPools:
  - name: ceph-blockpool
    spec:
      failureDomain: host
      replicated:
        size: 3
    storageClass:
      enabled: true
      name: ceph-block
      isDefault: false
      reclaimPolicy: Delete
      allowVolumeExpansion: true
      volumeBindingMode: Immediate
      parameters:
        imageFormat: "2"
        imageFeatures: layering
        csi.storage.k8s.io/provisioner-secret-name: rook-csi-rbd-provisioner
        csi.storage.k8s.io/provisioner-secret-namespace: "{{ .Release.Namespace }}"
        csi.storage.k8s.io/controller-expand-secret-name: rook-csi-rbd-provisioner
        csi.storage.k8s.io/controller-expand-secret-namespace: "{{ .Release.Namespace }}"
        csi.storage.k8s.io/controller-publish-secret-name: rook-csi-rbd-provisioner
        csi.storage.k8s.io/controller-publish-secret-namespace: "{{ .Release.Namespace }}"
        csi.storage.k8s.io/node-stage-secret-name: rook-csi-rbd-node
        csi.storage.k8s.io/node-stage-secret-namespace: "{{ .Release.Namespace }}"
        csi.storage.k8s.io/fstype: ext4
```

- A filesystem takes the same eight `*-secret-*` keys and `fstype`, with `rook-csi-cephfs-provisioner` and `rook-csi-cephfs-node`.
- Copy the full lists from `helm show values rook-release/rook-ceph-cluster --version <version>` and edit the copy.
- `parameters` cannot change after creation: delete the StorageClass and run `helm upgrade` again.

### A small cluster

The defaults ask for 4Gi per OSD and per metadata server and 1Gi per gateway
and mon, which suits dedicated storage nodes. On control planes that also run
workloads (about 12 GiB each), lower them. `cephClusterSpec.resources` is a map,
so write only the daemons you change. These numbers are a starting point, not a
sizing recommendation.

```yaml
cephClusterSpec:
  mon:
    count: 3
    allowMultiplePerNode: false
  resources:
    mgr: {limits: {memory: 1Gi}, requests: {cpu: 250m, memory: 512Mi}}
    mon: {limits: {memory: 1Gi}, requests: {cpu: 250m, memory: 512Mi}}
    osd: {limits: {memory: 3Gi}, requests: {cpu: 500m, memory: 2Gi}}
cephFileSystems:
  - name: ceph-filesystem
    # ...spec and storageClass written out in full, as above
    spec:
      metadataServer:
        resources: {limits: {memory: 1Gi}, requests: {cpu: 250m, memory: 512Mi}}
```

Do not run Rook on a schedulable control plane with a few GiB (3 GiB, say).
Its own pods use most of that, the kubelet evicts below 100 MiB free, and a
kube-apiserver static pod has been seen evicted during a Rook install. Taint
such a control plane `NoSchedule`, or add memory, first.

### Upgrade the cluster chart

The Rook operator edits the `CephCluster` after the chart creates it, and Helm 4
applies with server-side apply, so `helm upgrade` of `rook-ceph-cluster` can fail
with `conflict with "rook"`. Read the conflicting fields, then pass
`--force-conflicts` if your values should win.

## Drains

Ichor drains a node with the Eviction API, so PodDisruptionBudgets hold and a
refused eviction is retried until the step times out. With the chart's
`disruptionManagement.managePodBudgets: true`, Rook blocks OSD eviction and
unblocks it once the placement groups are `active+clean`. An Ichor drain of a
Ceph node therefore waits on Rook. With `managePodBudgets: false` nothing gates
an OSD, and Ichor does not either.

Removing an OSD, and the node that holds it, stays a Ceph procedure: mark it
out, wait for `active+clean`, purge it.

## Block volumes for KubeVirt and other non-root workloads

A pod running as a non-root user cannot open a `volumeMode: Block` volume
unless containerd gives it the device. By default the device stays root-owned,
so a KubeVirt VM or a CDI importer (uid 107) on a `ceph-block` PVC fails with
`blockdev: cannot open /dev/cdi-block-volume: Permission denied`.

Turn on containerd's `device_ownership_from_security_context`, so the device
follows the pod's `runAsUser` and `fsGroup`:

```sh
ichor set containerd-device-ownership true --cluster prod
```

The UI has the same switch under the cluster's details; in a manifest it is
`spec.containerd.deviceOwnershipFromSecurityContext: true`.

- Ichor renders one fixed containerd fragment; containerd settings are typed, one at a time, never raw TOML.
- containerd restarts on each node, with no reboot. Running pods keep their ownership: recreate a failed pod or DataVolume import.
- It needs `spec.versionContract` v1.14 or later. The default is `false`.

See [ADR 0172](../adr/0172-containerd-device-ownership-is-a-typed-cluster-setting-rendered-as-a-cri-customization.md).
