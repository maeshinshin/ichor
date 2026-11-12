# 0073. A read-only Talos details view on the machine page

Status: Accepted · Date: 2026-10-26 · Design section: UI backend, Talos access, Secrets
Decided: `NodeService.GetNodeTalosDetails` returns five independently failing sections (system, network, storage, services, extensions) read from the node over the existing read-only Talos client; every field is copied by name, nothing from config, certificates, keys or tokens is carried, and kernel arguments that could hold a credential are masked; an unapproved machine is read over the insecure maintenance API and flagged `maintenance_mode`

## Context

The machine page showed hardware, disks, links and usage. Operators also need to see what Talos itself reports: version, stage and unmet conditions, addressing, DNS and time, install disk and volumes, service health, extensions. The UI backend already dials each machine's Talos API read-only (`dialNode`: a `role.Reader` certificate from the bundle Secret for approved machines, the maintenance API otherwise). No new path to Talos is added.

## Decision

**RPC.** `GetNodeTalosDetails(machine)` in `NodeService`, messages in `proto/ichor/v1alpha1/talosdetails.proto`. It is authorised like `GetNode`: the TalosMachine and TalosCluster are read as the caller (impersonated with OIDC on), so a caller who cannot see the machine gets nothing from Talos.

**Independent sections.** The five sections are read concurrently, each with its own 10 second timeout. A failure puts a short message in that section's `error` field and drops any partial data of that section; the RPC itself still succeeds. The UI shows the error inline in that section.

**What is shown.**
- System: Talos version, architecture, platform and mode (from the machine API `Version`), machine stage with ready and unmet conditions (`MachineStatus`), hostname and domain, Kubernetes node name Talos registers, kernel arguments.
- Network: node addresses, links (name, MAC, operational state, MTU, speed, kind, driver), default gateways and route count, DNS resolvers and search domains, time servers, time sync state.
- Storage: disks (device, model, serial, size, type nvme/ssd/hdd/cdrom, install disk, read-only) and volume status (type, phase, location, mount location, size, filesystem, whether it is encrypted, error message).
- Services: id, state, health and last health message (machine API `ServiceList`).
- Extensions: name, version, author.

**What is deliberately not shown.** Machine configuration documents and the effective config (the page has its own masked view for that); certificates, keys, tokens and the secrets bundle; WireGuard private keys and peers; volume encryption keys, key slots and TPM options; the extension image reference; the bytes of `talos.config`-style kernel arguments. Mapping is an allowlist: each message is built field by field in `talosdetails_map.go` and `talosclient_details.go`, no Talos spec is marshalled whole, so a field Talos adds later cannot leak by accident. Kernel arguments are split and any whose key contains `token`, `secret`, `key`, `pass`, `auth`, `cred`, `config`, `siderolink` or `environment` keep their key and show `***` as value; the masking runs once, in the handler, the one boundary every `NodeTalosClient` implementation passes through. Also not shown, because there is no cheap read-only source: uptime, boot time and kernel version. Section error text is truncated to 300 bytes.

**Maintenance mode.** A machine that is not approved has no CA trust, so it is read over the insecure maintenance API, as `GetNode` already does, and the response sets `maintenance_mode`. Which calls the maintenance API accepts is up to Talos; a section it refuses shows its error inline, and the UI explains that the machine is read over the limited maintenance API and the rest appears once it is approved. Nothing is retried with other credentials.

**RBAC.** None changes: the RPC reads the same TalosMachine, TalosCluster and bundle Secret `GetNode` reads.

## Consequences

- One more read per page view against each node (five short calls); there is no polling.
- The `NodeTalosClient` interface grows five methods.
- `ichor` CLI's in-process fallback answers `Unavailable` for this RPC, like `GetNode`.
