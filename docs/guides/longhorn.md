# Longhorn on an Ichor cluster

Ichor prepares the Talos side of the nodes for Longhorn and holds node
operations until Longhorn's replicas have left the node. You install Longhorn
yourself. The [Storage](../design/architecture/storage.md) page describes the
layers.

![The Longhorn flow: apply a TalosStorage, approve the extensions operation, roll out the config, install the chart with Helm, then StorageSystemConsistent is True. A node operation waits for replicas to leave the node, so keep more storage nodes than replicas.](../images/longhorn-flow.light.drawio.svg)

*The flow from a TalosStorage to a consistent Longhorn, and the replica rule.*

## What Ichor does and does not do

| Ichor does | Ichor does not |
| --- | --- |
| Add the `iscsi-tools` and `util-linux-tools` extensions. | Install Longhorn, its StorageClass or its volumes. |
| Mount the chosen disk at `/var/mnt/longhorn`. | Choose the disk for you: `diskSelector` is required. |
| Label and annotate the nodes so Longhorn registers the disk. | Label the `longhorn-system` namespace for Pod Security. |
| Wait for replicas to leave a node before an operation. | Pick the replica count. |

## Prerequisites

- Kubernetes 1.34 or later. Longhorn's installation requirements (docs 1.14.0)
  say "Kubernetes >= v1.34".
- A spare disk on each storage node, such as `/dev/vdb`.
- Longhorn installed in the `longhorn-system` namespace. Ichor's evacuation hook
  reads Longhorn's objects there and nowhere else.

## Prepare the nodes

1. Label the machines in `spec.nodeLabels` (here `storage: longhorn`), then
   apply the `TalosStorage`:

   ```yaml
   apiVersion: ichor.maesh.dev/v1alpha1
   kind: TalosStorage
   metadata:
     name: longhorn
     namespace: <the TalosCluster's namespace>
   spec:
     clusterRef:
       name: my-cluster
     nodes:
       matchLabels:
         storage: longhorn
     diskPreset: longhorn-v1
     diskSelector: disk.dev_path == "/dev/vdb"
   ```

2. Ichor adds the two extensions through a paused upgrade named
   `storage-extensions-…`. Approve it:

   ```sh
   ichor approve operation storage-extensions-<suffix>
   ```

3. Roll the config out:

   ```sh
   ichor rollout config
   ```

| Result | Condition |
| --- | --- |
| Talos side prepared | `Ready=True`, reason `Prepared` |
| Extensions in the installer image | `ExtensionsPresent=True`, reason `AllPresent` |

The disk is mounted at `/var/mnt/longhorn`. The preset labels each node
`node.longhorn.io/create-default-disk=config` and annotates it with the disk
path.

`spec.nodes` matches `spec.nodeLabels` on the machine. `diskSelector` is required
for `longhorn-v1`; the API refuses a `TalosStorage` without it.

## Install Longhorn

1. Allow privileged pods in the namespace. Longhorn requires pod security
   `enforce: "privileged"`:

   ```sh
   kubectl create namespace longhorn-system
   kubectl label namespace longhorn-system pod-security.kubernetes.io/enforce=privileged
   ```

2. Install the chart with the setting that makes Longhorn use the labelled
   nodes:

   ```sh
   helm install longhorn longhorn/longhorn -n longhorn-system \
     --set defaultSettings.createDefaultDiskLabeledNodes=true
   ```

3. Wait for `StorageSystemConsistent`.

| `StorageSystemConsistent` | Meaning |
| --- | --- |
| `Unknown`, reason `NotInstalled` | Longhorn is not there yet. Not an error. |
| `True`, reason `Consistent` | Longhorn matches the preset. |
| `False` | Longhorn disagrees with the preset, for example "Create Default Disk on Labeled Nodes" is off. The message says what. |

Pin the chart version you read the Longhorn documentation for.

## Volumes

| Mode | Works | Notes |
| --- | --- | --- |
| RWO | Yes | |
| RWX | Yes | Longhorn's share-manager and Talos's NFS client. No extra extension. |

## Node operations and replicas

A reboot, an upgrade or a drain of a storage node first waits for Longhorn's
replicas to move off it. If they do not move within the evacuation timeout, the
operation pauses with:

```text
evacuating "NODE" timed out with storage replicas still remaining
```

| Setting | Value |
| --- | --- |
| Timeout | `TalosStorage.spec.evacuationTimeout`, default 10 minutes |
| Replicas equal storage nodes (3 on 3) | Replicas have nowhere to go: the operation pauses |
| Storage nodes exceed replicas (4 for 3) | Replicas move: the operation goes on |

Either keep more storage nodes than the replica count, or lower the replica
count.

## Drains

- Ichor evicts pods through the Eviction API, so PodDisruptionBudgets apply.
- A pod with no controller is refused: "cannot delete Pods that declare no
  controller".
- Run workloads under a Deployment or StatefulSet.
