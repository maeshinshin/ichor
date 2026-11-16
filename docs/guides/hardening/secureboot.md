---
sidebar_order: 6
---

# SecureBoot, UKI and kernel lockdown

```yaml
spec:
  security:
    secureBoot: {mode: Preferred}      # Off (default), Preferred or Required
    kernelLockdown: integrity          # or confidentiality; checked, not rendered
```

Talos (v1.14, "SecureBoot") boots a signed Unified Kernel Image through
systemd-boot, so the firmware verifies kernel, initramfs and command line. It
needs UEFI (not BIOS) and a fresh install from the SecureBoot images: Talos
cannot turn a GRUB install into a UKI one. Image Factory serves the installer as
`factory.talos.dev/metal-installer-secureboot/<schematic>:<version>` and the
ISO as `metal-amd64-secureboot.iso`.

## What the modes do

- `Off` (or unset): nothing changes. A node that nevertheless runs SecureBoot is
  still upgraded with the SecureBoot installer, because the stock one writes an
  unsigned UKI the firmware would refuse; its `SecureBoot` condition says the
  mode and the node disagree.
- `Preferred`: where Ichor picks the installer it picks the SecureBoot variant
  (a Talos upgrade of a node that booted a UKI; an approval that names a disk
  and no `--install-image`, which then records
  `factory.talos.dev/metal-installer-secureboot/<spec.schematic or the stock
  schematic>:<spec.talosVersion>` in `spec.install.image`). Machines without
  SecureBoot are reported and left alone.
- `Required`: as `Preferred`, and also: a TPM disk key renders
  `checkSecurebootStatusOnEnroll: true` unless `requireSecureBoot: false` says
  otherwise; the webhook refuses to set `Required` while an approved machine does
  not run SecureBoot, unless the cluster carries
  `ichor.maesh.dev/confirm-secureboot-reinstall: "true"`; and the controller does
  not send the first configuration to a machine in maintenance mode that booted
  without SecureBoot (`Ready=False`, reason `SecureBootRequired`, nothing sent).
  A node that is already installed without SecureBoot is reported
  (`SecureBoot=False`), never reconfigured or wiped.

## Reading the state

Every node's state is read from Talos (`talosctl get securitystate`) and kept in
`TalosMachine.status.security`; the conditions `SecureBoot` and `KernelLockdown`
exist on each machine and, summarised, on the cluster while the option is set.
The UI's node page (Talos details, System) shows the same fields live. The
condition reasons: `SecureBootOn`, `SecureBootOff` (a UKI node whose firmware has
SecureBoot disabled), `NotUKI` (a GRUB install), `ModeOffButSecureBoot`.

## Lockdown

In Talos v1.14 SecureBoot images run with `lockdown=integrity`
implicitly and no longer force `lockdown=confidentiality`. The command line is
embedded in the UKI and ignored at boot, and `machine.install.extraKernelArgs` is
refused with the UKI command line, so Ichor cannot render a lockdown level: for
`confidentiality` create an Image Factory schematic with
`extraKernelArgs: [lockdown=confidentiality]` and set `spec.schematic.id` to it.
Ichor reads the node's command line and reports the level in effect
(`status.security.kernelLockdown`: the last `lockdown=` word, else `integrity`
under SecureBoot, else `none`).

## Moving an installed cluster

Changing the mode never changes an installed
node, for three reasons Talos gives: a GRUB install cannot become UKI, key
enrolment happens in the firmware, and a TPM disk key is bound to the SecureBoot
state (PCR 7). The path, one node at a time, as for disk encryption: set the
mode (and the confirm annotation for `Required`); take an etcd backup
([Back up etcd to S3](../etcd-backup.md)); remove the
node with a full system-disk wipe, `ichor delete node NODE --wipe-mode system-disk --reboot --override --confirm MACHINE`
(or choose "The whole system disk" under "What to wipe" in the UI's Remove node
dialog); boot the machine
from the SecureBoot ISO of this cluster's schematic and Talos version
with the firmware in setup mode (the keys are enrolled automatically; on bare
metal choose "Enroll Secure Boot keys: auto" in the boot menu); approve it again;
wait for `SecureBoot=True` before the next node, control planes one at a time
with etcd quorum intact.

> [!WARNING]
> Keep the UKI and PCR signing keys if you build your own
> images: losing them stops the next UKI booting and the encrypted disks unlocking.

## Why the default wipe is not enough here

A remove without `--wipe-mode` wipes
only STATE and EPHEMERAL. The installed boot partition stays,
so a machine whose system disk comes first in the boot order boots its installed
UKI again, into maintenance mode, and the SecureBoot ISO is never started: nothing
is reinstalled, the node only takes the configuration again. `--wipe-mode system-disk` (`wipeMode: system-disk` on a reset operation)
erases the whole system disk, boot and EFI included, so the firmware falls through
to the ISO; `--wipe-mode all` erases the same system disk and leaves user disks alone.
`--wipe-mode system-disk-and-user-disks` erases the system disk and every user disk (use it only when their
data is expendable). Keep the ISO attached or first in the boot order before you
remove the node. Without Ichor's wipe, the same effect comes from
`talosctl reset --wipe-mode system-disk` through the admin path (Ichor's `system-disk`
sends the same Talos reset mode; do not read Ichor's `all` as `talosctl reset --wipe-mode all`,
which the Talos documentation does not define beyond its name), or from putting the ISO
first in the boot order. After the wipe the machine is looked for again every 15
seconds for 3 minutes, so it shows up as a pending machine soon after it has
booted. The remove deletes the node's Kubernetes Node object.
