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

```yaml
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
`spec.nodeLabels`, not the labels on the Kubernetes Node. `spec.diskSelector` is
not needed for `rook-v1`; the preset ignores it if you set it.

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
