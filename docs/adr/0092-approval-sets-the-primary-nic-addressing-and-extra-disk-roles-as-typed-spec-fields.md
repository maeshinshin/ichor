# 0092. Approval sets the primary NIC, its addressing and extra disk roles as typed spec fields

Status: Accepted · Date: 2026-10-27 · Design section: Nodes (approval, disk roles)

Decided: `TalosMachine.spec.network` (one primary link chosen by permanent MAC, DHCP or static addressing, optional VLAN, DNS servers) and `TalosMachine.spec.disks` (roles `ephemeral` and `userVolume` on disks identified like `spec.install.disk`) are written at approval by the UI form and `ichor approve node` flags; `pkg/render` emits a `LinkAliasConfig` alias `net0` with a CEL selector on the permanent MAC and refers to the alias from `DHCPv4Config`, `LinkConfig`, `VLANConfig` and `ResolverConfig`, a `VolumeConfig` for EPHEMERAL and a `UserVolumeConfig` per volume, and the legacy `machine.network.interfaces` before the multi-document contract; link addressing documents are classified live in `pkg/fieldmeta`, aliases and everything else keep the reboot-required default; bonds, the `storage` role and `unused` are not done

## Context

ADR 0077 gave approval the system disk and left the rest of the design's approval step ("role, labels, disk roles and
basic networking") without a field to carry it, so the form could not say which NIC to use or how to address it, and a
node with a second disk could not put EPHEMERAL or a data volume on it.

## Decision

1. `spec.network {mac, addressing, addresses, gateway, dnsServers, vlanID}`. `mac` is the link's permanent address
   (pattern in the CRD). `addressing` is `dhcp` (default) or `static`; static needs at least one address (`isCIDR`) and
   dhcp takes no addresses or gateway (CEL on the struct). `gateway` and each DNS server are `isIP`. `vlanID` is 1 to 4094.
   Unset leaves Talos's default. The field is mutable in the CRD; what a change costs is the fieldmeta classification
   below, not an admission rule.
2. `spec.disks[] {role, disk, name, maxSize}`. `role` is `ephemeral` or `userVolume`; `disk` is the same
   one-of selector as `spec.install.disk`. A user volume needs a `name` (letters, digits, hyphen, at most 32, mounted at
   `/var/mnt/<name>`) and may carry `maxSize` (`200GiB`, MiB/GiB/TiB). At most eight entries, one `ephemeral`, unique
   names (CEL). Whether the system disk and an `ephemeral` disk differ is not expressible in CEL (the two selectors can
   name one disk differently), so the UI checks it and Talos decides otherwise.
3. Network rendering, from the multi-document contract (`MultidocNetworkConfigSupported`, Talos above 1.11, so both v1.13
   and v1.14):
   - `LinkAliasConfig` `net0`, selector `mac(link.permanent_addr) == "<lower-case mac>"`, parsed against
     `celenv.LinkLocator()` so a wrong expression fails at render. This is the fix for interface names changing.
   - DHCP: `DHCPv4Config` named by the alias (IPv4 only; DHCPv6 is not requested).
   - Static: `LinkConfig` for `net0` with `up: true`, the addresses, and a default route when a gateway is set.
   - VLAN: the alias gets a `LinkConfig` with `up: true` and nothing else, and a `VLANConfig` `net0.<id>` with
     `parent: net0` carries the addresses and routes, or has a `DHCPv4Config` of its own.
   - DNS servers: a `ResolverConfig` `nameservers` list, merged into the generator's own `ResolverConfig`.
   Contract 1.11 and older render one `machine.network.interfaces` entry with `deviceSelector.permanentAddr`, the VLAN
   under it, and `machine.network.nameservers`; the legacy default route is `0.0.0.0/0` or `::/0` by gateway family.
4. Disk rendering: a `VolumeConfig` named `EPHEMERAL` whose `provisioning.diskSelector.match` is the same CEL expression
   `spec.install` uses (`diskExpression`), which replaces the generator's own EPHEMERAL document by merge; a
   `UserVolumeConfig` per volume with that selector, `grow: true`, `maxSize` if given, otherwise `minSize: 1GiB` because
   Talos refuses a user volume with neither. These are emitted for every contract, as the documents exist well before
   1.12.
5. fieldmeta (`pkg/fieldmeta`), read against the Talos v1.14.3 controllers in
   `internal/app/machined/pkg/controllers/network`: `LinkConfig`, `VLANConfig` and `DHCPv4Config` are **safe**
   (`link_config.go`, `address_config.go`, `route_config.go`, `operator_config.go` take the active `MachineConfig`
   as a weak input and rewrite their specs on every change, and `link_spec.go`, `address_spec.go`, `route_spec.go`,
   `operator_spec.go` program the kernel); `ResolverConfig.nameservers` is safe on the same grounds as the existing
   `machine.network.nameservers` rule (`resolver_config.go`). `LinkAliasConfig` is left at the reboot-required default
   although `link_alias_config.go` also reacts live, because it renames the handle every other document uses.
   `ResolverConfig.hostDNS` and the volume kinds are unchanged: `VolumeConfig.provisioning` is already install-time-only
   and `UserVolumeConfig` keeps the default, as the design says a new user volume on an unused disk needs no reset.
   "Safe" is about the reboot, not about reachability: an address change on the link the controller dials moves the node
   (ADR 0086 covers re-finding it).
6. Approval. `ApproveMachineRequest.network` and `.disks`, mirrored on `Machine`; `TalosLink.permanent_mac` is new on
   the wire (the details RPC showed the current MAC, which differs for a bond member or a spoofed address). The backend
   re-checks everything the CRD checks and requires the MAC to be one of the machine's discovered identity MACs, so a
   typo is `InvalidArgument` with the reason. CLI: `--nic MAC`, `--address CIDR` (repeatable; any address makes it
   static), `--gateway`, `--dns` (repeatable), `--vlan`, `--ephemeral-disk DISK`, `--user-volume NAME=DISK[,size=200GiB]`
   (repeatable), with DISK in the `--install-disk` syntax. UI: a card picker listing physical links as name, MAC and
   speed (a typed MAC if the node reports none), address fields, and disk pickers from the disks `GetNode` reads.
7. Layer. Network and disk documents are system layer, applied with the install document and below every patch, so a
   `TalosConfigPatch` can still override them.

## Not done

- Bonds. The design says a bond takes members by name or alias and a MAC form emits one alias per member; that needs a
  second link, a mode and a `BondConfig`, and is its own change.
- The `storage` role (needs a `TalosStorage` preset, ADR 0050), `unused` (no document to render) and `RawVolumeConfig`.
- Whether Talos still runs its default DHCP on a link that has a `LinkConfig` but no `DHCPv4Config` is not known;
  static addressing is rendered as written. The documents are validated by Talos's own machinery at render time.
- The "accepted, no effect" report for an edited EPHEMERAL selector on an installed node (design, Nodes) is not part
  of this change; `VolumeConfig.provisioning` is already classified install-time-only.
- The details RPC only reports links of a maintenance-mode node through the insecure API, so a node whose Talos API is
  unreachable gets a typed MAC.
