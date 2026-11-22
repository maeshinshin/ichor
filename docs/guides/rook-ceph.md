# Prepare Talos nodes for Rook/Ceph

Ichor prepares the Talos side of a node for Rook/Ceph and nothing else. Rook,
Ceph and the choice of disks stay with you. This page covers the Talos side:
which disk a node leaves for Rook, the one case where Talos must carve a
partition, and the settings in Ichor and in the Rook chart that make the
node ready.

## What Ichor does and does not do

Ichor does:

- Render a `TalosStorage` with `diskPreset: rook-v1` as a node that leaves its
  disks alone. Rook then consumes the raw device. The
  [Storage](../design/architecture/storage.md) page describes the layers.
- Render a labelled raw partition on a node if you write one as a
  `TalosConfigPatch` (option B below).
- Exempt the `rook-ceph` namespace from Pod Security, through
  `TalosCluster.spec.security.podSecurity.exemptNamespaces`.
- Gate node drains through Rook's own disruption budgets, which it does by
  itself (see [Drains](#drains)).

Ichor does not:

- Install Rook, the Rook operator, the Ceph-CSI drivers, the `CephCluster`, the
  pools or the StorageClass. Those are the Rook charts and your own manifests.
- Choose which devices become OSDs. Name them yourself (see
  [Name the devices](#name-the-devices)).
- Run Ceph's own OSD procedures: `ceph osd out`, waiting for the placement
  groups to be `active+clean`, and purging an OSD. Those belong to Rook and Ceph.

## The disk must be unformatted

Rook's prerequisites at v1.19.6 say that at least one of these is required
(https://github.com/rook/rook/blob/v1.19.6/Documentation/Getting-Started/Prerequisites/prerequisites.md):

> * Raw devices (no partitions or formatted filesystems)
> * Raw partitions (no formatted filesystem)
> * LVM Logical Volumes (no formatted filesystem)
> * Persistent Volumes available from a storage class in `block` mode

Rook's page also says that `lsblk -f` shows a filesystem on a device when its
`FSTYPE` is not empty, and that such a device is not available to Rook. Use
the raw device, or a raw partition, and nothing else.

LVM is needed only for encryption, for a `metadata` device, or for
`osdsPerDevice` greater than 1. Ichor does not configure LVM for Rook.

> [!WARNING]
> Do not give Rook a `UserVolumeConfig`. Talos formats a user volume, and the
> Talos v1.14 reference gives `xfs` as the default filesystem type. Rook cannot
> use a formatted disk.

## Option A: leave the disk alone (`rook-v1`)

This is the default. The node has no Talos volume for the disk, so Talos does
not partition or format it, and Rook takes the device.

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

`spec.nodes` matches the labels the machine declares in its own
`spec.nodeLabels`, not the labels on the Kubernetes Node. Leave
`spec.diskSelector` out for `rook-v1`: the API refuses it, because the preset
writes no disk config and a selector would do nothing.

The disk must already be free of partitions and filesystems. Ichor does not
clean a disk that a previous system left behind.

## Option B: a raw partition

Use this when you want Talos to carve a partition, for example to keep Rook off
the whole disk. Write the partition as a `TalosConfigPatch`, in Ichor's typed
settings, at the `selector` layer:

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

Talos's own raw volume page (Talos v1.14) says:

> Raw Volumes allow allocating an unformatted partition, label it, and make it available for use by advanced storage drivers like Container Storage Interface (CSI) drivers.

and says that the label is `r-<volume-name>`. The volume name must be 1 to 34
ASCII letters, digits or `-`.

> [!WARNING]
> Ceph will not create a partition if the partition label contains the
> substring `ceph`. Avoid using such names for your labels.

So `osd-data` is a valid name and `ceph-osd-data` is not: the second one's
label, `r-ceph-osd-data`, contains `ceph`, and Ceph will not create the
partition.

Talos keeps the partition's data when you remove the patch. The same page says
"The actual disk data hasn't been removed yet, so you can re-apply the raw volume
configuration back and it will be re-provisioned on the same disk", and
wiping is a separate step (`talosctl wipe disk ... --drop-partition`).

Talos creates a stable symlink for the partition, `/dev/disk/by-partlabel/r-osd-data`.
Whether Rook's device selection accepts that path form is not established by this
guide; check it against the Rook version you run, and name the partition the same
way as any other device in [Name the devices](#name-the-devices).

## Keep the namespace out of Pod Security

Ceph's daemons need privileged access. Exempt the `rook-ceph` namespace in the
`TalosCluster`:

```yaml
spec:
  security:
    podSecurity:
      exemptNamespaces: [rook-ceph]
```

Ichor adds the namespace to the exempt list; `kube-system` stays exempt. A
change to Pod Security restarts the kube-apiserver on each control plane and
does not reboot a node; see [Pods and the API server](hardening/pods-and-api.md).

## Data stays under `/var`

Rook keeps its configuration and mon data in `dataDirHostPath`, which the
`rook-ceph-cluster` chart defaults to `/var/lib/rook`. Talos keeps `/var` on
the EPHEMERAL partition, and it is writable. Ichor mounts nothing extra for
Rook.

The Talos v1.14 architecture page says that content under `/var`
"survives machine reboots and on machine upgrades, but it is wiped and lost on
resets". A reset of a node therefore removes that node's copy of this directory,
so plan resets with the mons in mind.

Rook's cluster documentation also forbids `/etc/ceph`, `/rook` and
`/var/log/ceph`, and their subpaths, as `dataDirHostPath`.

## Name the devices

> [!WARNING]
> The `rook-ceph-cluster` chart defaults to `useAllNodes: true` and
> `useAllDevices: true`. Rook's cluster documentation says of `useAllDevices`:
> "**Not recommended** unless you have a very controlled environment where you
> will not risk formatting of devices with existing data. When `true`, all
> devices and partitions will be used."

Turn both off and name the nodes and the devices in the chart values:

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

Rook documents `devices` as "A list of individual device names belonging to
this node to include in the storage cluster." Rook's `deviceFilter` and
`devicePathFilter` are regular expressions over the same names and paths; a
filter that matches a disk you did not mean to give Ceph is the same mistake.

## Install the charts

Install the `rook-ceph` chart, then the `rook-ceph-cluster` chart, in the
`rook-ceph` namespace. Pin the chart version you read the documentation for;
the examples here are for v1.19.6:

```sh
helm install --create-namespace -n rook-ceph rook-ceph rook-release/rook-ceph --version v1.19.6
helm install -n rook-ceph rook-ceph-cluster rook-release/rook-ceph-cluster --version v1.19.6 -f values.yaml
```

### Restate the StorageClass parameters

Helm merges maps from your values file into the chart's defaults, but it
replaces a list whole. `cephBlockPools`, `cephFileSystems` and
`cephObjectStores` are lists. If your values file overrides one of them, the
chart's default entry is gone, including its `storageClass.parameters`: the
`csi.storage.k8s.io/*` names of the Ceph admin Secrets. A StorageClass rendered
without them provisions nothing, and a claim stays `Pending` with
`provided secret is empty`.

So write the parameters in every entry you override. For a block pool:

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

For a filesystem, use the same eight `csi.storage.k8s.io/*-secret-*` keys and
`csi.storage.k8s.io/fstype`, with `rook-csi-cephfs-provisioner` and
`rook-csi-cephfs-node` as the Secret names. Take the full default lists from
`helm show values rook-release/rook-ceph-cluster --version <version>` and edit
a copy, so nothing else is dropped.

A StorageClass's `parameters` cannot be changed after it is created. To fix one
that was rendered without them, delete the StorageClass and run
`helm upgrade` again.

### A small cluster

The chart's defaults ask for 4Gi of memory for each OSD, 4Gi for each metadata
server and 1Gi for each gateway and mon, and are meant for dedicated storage
nodes. On control planes that also run workloads, with about 12 GiB of memory
each, lower the requests and the limits in your values file:

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

`cephClusterSpec.resources` is a map, so it merges with the chart's default:
write only the daemons you change. These numbers are a starting point for a
small cluster, not a sizing recommendation.

Do not run Rook on a schedulable control plane with only a few GiB of memory
(3 GiB, say). The control plane's own pods (kube-apiserver, etcd, Cilium) already
use most of it, and the kubelet evicts pods when less than 100 MiB is available.
A control plane that runs short of memory fails its kube-apiserver liveness
probe and restarts it, and a kube-apiserver static pod has been seen evicted
during a Rook install on one. Taint such a control plane `NoSchedule`, or give
it more memory, before you install the chart.

### Upgrade the cluster chart

The Rook operator edits the `CephCluster` and the other custom resources after
the chart creates them. Helm 4 applies charts with server-side apply, so a
`helm upgrade` of `rook-ceph-cluster` can stop with `conflict occurred while
applying object ... Apply failed with N conflicts: conflict with "rook"`.
`helm upgrade --help` describes the flag for this case:

> `--force-conflicts`  if set server-side apply will force changes against conflicts

Pass `--force-conflicts` to `helm upgrade` once you have read what the
conflicting fields are and decided that your values should win.

## Drains

Ichor drains a node by cordoning it and evicting its pods through the Eviction
API, so PodDisruptionBudgets are respected and a refused eviction is retried
until the step times out. Rook's managed budgets then decide when an OSD may go.

Rook's cluster documentation at v1.19.6 says that with
`disruptionManagement.managePodBudgets` set to `true`, "The operator will block
eviction of OSDs by default and unblock them safely when drains are detected."
The chart sets it to `true`. Rook's design for the budgets creates blocking
budgets for the other failure domains during a drain, and restores the default
once "all the pgs are `active+clean`".

So an Ichor drain of a Ceph node waits on Rook, and no Ceph step is needed in
Ichor. If you set `managePodBudgets: false`, nothing gates an OSD's eviction,
and Ichor will not gate it either.

Removing an OSD, and the node that holds it, is still a Ceph procedure: mark the
OSD out, wait for `active+clean`, then purge it. Ichor does not do this for you.

## Using Block volumes with KubeVirt or other non-root workloads

A pod that runs as a non-root user cannot open a `volumeMode: Block` volume unless
containerd gives the device to that user. By default containerd leaves the device
owned by root, so a KubeVirt virtual machine or a CDI importer (both run as uid 107)
fails on a Rook `ceph-block` PVC with `blockdev: cannot open /dev/cdi-block-volume:
Permission denied`.

Turn on containerd's `device_ownership_from_security_context`, which makes the
device follow the pod's `runAsUser` and `fsGroup`:

```sh
ichor set containerd-device-ownership true --cluster prod
```

The UI has the same switch under the cluster's details. In a manifest it is
`spec.containerd.deviceOwnershipFromSecurityContext: true`.

- Ichor renders one fixed containerd fragment into every node. You cannot give it
  other TOML: containerd settings are typed, one at a time.
- containerd restarts on each node; no node reboots. Pods that are running keep their
  device ownership, so recreate a pod (or its DataVolume import) that failed before.
- It needs `spec.versionContract` v1.14 or later.
- The default is `false`: a cluster that does not set it is unchanged.

The decision is recorded in [ADR 0172](../adr/0172-containerd-device-ownership-is-a-typed-cluster-setting-rendered-as-a-cri-customization.md).
