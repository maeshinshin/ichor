# 0138. A config patch carries a ResolverConfig document, and the kind allow-list grows by one generated entry per kind

Status: Accepted · Date: 2026-11-10 · Design section: Configuration · Amends: 0101

Decided: `TalosConfigPatch.spec.documents[]` accepts `kind: ResolverConfig` (field `resolverConfig`), a typed mirror of Talos's `ResolverConfig` document. A patch that carries one is refused for a machine whose `spec.network.dnsServers` already renders a ResolverConfig. The same recipe, one line in mirrorgen's `roots` plus the generated and marker edits listed below, adds every further kind.

## Context

A real Talos v1.13.1 cluster could not be adopted: `ichor adopt --plan` blocked every machine on `ResolverConfig.apiVersion`, `.kind` and `.nameservers`. The allow-list had no way to state them, and the legacy `machine.network.nameservers` is no substitute: machinery's `ResolverConfigV1Alpha1.V1Alpha1ConflictValidate` refuses a config holding both, and dropping the document leaves the adopt diff non-empty. About 45 more document kinds are in the same position.

## Decisions

1. **Extend the allow-list, typed only.** A kind is added by one entry in mirrorgen's `roots` (`internal/tools/mirrorgen/main.go`), which generates the mirror type and both conversions, plus: the package's import alias (`walk.go`, `emit.go`) and its `packages.Load` entry if the package is new, the `kind` enum and the two XValidation rules and the typed field in `talosconfigpatch_types.go`, a case in `render.DocumentFromPatch`, a line in `uiapi.documentFieldKey`, and a round-trip test. `TestAllowListMatchesCRDEnum` fails if the enum and `roots` disagree. Field types the walker cannot mirror structurally go in `substTable`, and a name clashing with an existing mirror in `typeRenames` (here `network.HostDNSConfig` becomes `ResolverHostDNSConfig` and `nethelpers.DNSProtocol` is a scalar). There is still no raw-YAML escape hatch: a document Ichor cannot type is a document Ichor cannot patch, so the schema, the webhook and `fieldmeta` keep seeing every field.
2. **Secret material is never mirrored.** A kind that holds a secret (a key, token, password, or any value that mirrors the secrets bundle) is either not added or has the field dropped through mirrorgen's `secretFields` table, so it cannot reach a CRD or etcd in clear text; credentials stay in Secrets referenced by name (ADR 0101). ResolverConfig holds none (addresses, TLS server names, search domains).
3. **One owner for the DNS servers.** `spec.network.dnsServers` renders a ResolverConfig (and the generator emits one from v1.14). A patch ResolverConfig would be merged onto it by list position, so the result would depend on patch order. Render refuses it instead: "patch X carries a ResolverConfig but machine M sets spec.network.dnsServers". Because the render fails only with the patch, the patch webhook refuses the patch too, naming the machine. The way out is to set the servers in one place. A machine without `dnsServers` takes the patch as it takes any other.
4. **Impact.** `ResolverConfig.apiVersion` and `.kind` are identity fields that appear in a diff exactly when the document appears; Talos applies the nameservers live, so they are `safe` like `ResolverConfig.nameservers`, and a patch that only sets nameservers needs no reboot. `hostDNS` and `searchDomains` keep the reboot-required default.
5. **Adopt.** `specFieldHints` names both places nameservers can be written.

## Rejected

- Refusing nothing and letting the later writer win: the merge of two lists is positional, not a deterministic "last wins".
- Making the patch win over `dnsServers`: it would make a spec field silently ineffective.
- Dropping `spec.network.dnsServers`: CRD fields are append-only.

## Risks

Every kind adds its schema to the TalosConfigPatch CRD and to the TalosCluster CRD that embeds patches (ResolverConfig: about 6 kB each), toward the 1.5 MB etcd object limit.
