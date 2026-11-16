---
sidebar_order: 5
---

# Disk encryption

```yaml
spec:
  security:
    diskEncryption: {type: tpm, requireSecureBoot: true}   # or nodeID, or kms + kmsEndpoint
```

Renders `VolumeConfig` documents for STATE and EPHEMERAL with a LUKS2 key slot 0
(Talos v1.14, "Disk Encryption"), EPHEMERAL's key locked to STATE. The key type:

- `tpm`: sealed in the machine's TPM 2.0, bound to PCR 7. `requireSecureBoot`
  makes enrolment fail unless SecureBoot is on; without SecureBoot the TPM key
  gives no boot-chain protection.
- `nodeID`: derived from the node UUID. It protects against a removed disk only.
- `kms`: a KMS server seals and unseals the key. `kmsEndpoint` must be `https://`;
  Ichor refuses any other scheme because the key material would cross the network
  in plaintext.

Static passphrases are not available (they would sit in the machine config).

## Nodes without a TPM

Ichor does not check that a node has a TPM: it records no hardware inventory.
Talos refuses to enrol a TPM key on a machine without one: on a node
without a TPM the node installs, reboots and stays in stage `booting` with the
STATE volume `failed`. Ichor puts the failed volume and Talos's error in the
`Ready` message (`NodeStarting: ... stage "booting" ...; a volume failed:
STATE: stat /dev/tpm0: no such file or directory`).

## Existing machines

> [!WARNING]
> Talos encrypts a volume only when it creates it, so
> setting this on a cluster that already has approved machines changes nothing on
> them until each one is wiped and rejoined.

The webhook therefore refuses the
change unless the `TalosCluster` carries the annotation
`ichor.maesh.dev/confirm-disk-encryption-wipe: "true"`. Ichor never wipes a node
for this. The path, one node at a time: set the option and the annotation, then
remove a worker, wait for it to rejoin and come up encrypted, then do each
control plane in turn, leaving etcd quorum intact. Back up etcd first.

### Remove the node

"Reset
and add" is for machines of another cluster and refuses one already
in Ichor; for your own node use `ichor delete node NODE --reboot --override --confirm MACHINE`
(NODE is the machine's name or its hostname; `--confirm` is always the machine name, the
TalosMachine's UUID that the prompt and the UI dialog show),
and approve the pending machine again once it is rediscovered.

### Pods that block the drain

Pods that no
controller manages (bare pods) block the drain: the machine's `Removal`
condition says `DrainFailed` and names the pod, a Warning Event repeats it,
and the drain is retried with growing backoff, so delete the pod (there is no
force option; Ichor never deletes a pod nobody owns) and it carries on. The
remove deletes the Kubernetes Node object once the node is not Ready.

### Confirming the wipe

With `--reboot` the
removal also waits, up to five minutes, for the machine to answer in maintenance mode with its
UUID, which is how it confirms the wipe; if the machine does not answer, the removal completes
and a Warning Event `NodeStillHasClusterCredentials` says the wipe is not confirmed. Without
`--reboot` the machine is powered off after the wipe and cannot answer, so the wipe is not
confirmed, so the removal completes once the Node is NotReady, and a Warning Event
`WipeNotConfirmedPoweredOff` says why. If the Node is still Ready after five minutes, Ichor keeps it, marks
it so that discovery no longer counts its address as a member and finds the wiped
machine again, and a Warning Event names `kubectl delete node`.

The reset takes some tens of seconds, and
the node is Ready again a few minutes after approval; the volumes show `encryptionProvider: luks2`
in `talosctl get volumestatus`.
