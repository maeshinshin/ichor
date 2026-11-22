# Working on Ichor

Read `docs/design/architecture.md` first; it's the source of truth. Conventions
are in `CONTRIBUTING.md`.

Before writing code, ask whether it needs to exist, whether the standard
library or an installed dependency already does it, and whether it can be one
line. Mark a deferred shortcut with a `shortcut:` comment naming its limit and
when to drop it.

Rules that are easy to get wrong:

- Work inside `nix develop`. Go must be 1.27.4 or newer. Add missing tools to
  `flake.nix`; don't install them any other way.
- Scaffold with the tools (`kubebuilder`, `cobra-cli`, `npx shadcn@latest add
  @preactcn/…`), never by hand. Build and generate with the Makefile targets
  (`make help` lists them). Never call controller-gen/kustomize/envtest
  directly.
- Read the documentation for the exact library version in use before writing
  code against it. Don't code from memory.
- Don't read or reference Sidero Omni — code, docs, release notes or UI. This
  is a clean-room implementation.
- Machine config is typed only (`TalosConfigPatch.spec.config` with the
  Talos machinery types). No raw-YAML escape hatch.
- UI: preactcn only. No React, no `preact/compat`.
- The controller is the only writer to Talos nodes, apart from three CLI commands
  that cannot wait for it: `init`, `restore cluster` and `reclaim node --talosconfig`.
  Nothing else, and nothing in the UI, writes to a node; a fourth writer is a design
  change, not a shortcut. UI and CLI otherwise change the world by writing CRs.
- The controller never mutates `spec`. There are two recorded exceptions to that, and
  a third narrow writer of metadata; each is a separate ServiceAccount the controller
  impersonates, with a webhook limit on what it may write: `ichor-auto-patch` (a newer patch of the running minor in
  `spec.talosVersion` and `spec.kubernetesVersion`, ADR 0116) and
  `ichor-storage-extensions` (`spec.schematic.id`, from unset or the stock schematic,
  to a schematic with the extensions a storage preset needs; `patch` on `talosclusters`
  only, ADR 0173). The third writes no spec: `ichor-consent-sweep` removes the
  `ichor.maesh.dev/confirm-kube-proxy-switch` annotation once no switch needs it
  (`patch` on `talosclusters` only, ADR 0066). A fourth is a design change, not a
  shortcut.
- Ichor owns Cilium (its only CNI) as a Helm release. It does not install or
  operate Argo CD, Kokumi, Flux or any GitOps tooling, nor storage or
  monitoring.

`kubebuilder init` wants an empty directory: on a fresh repo run it first
(inside `nix develop`), commit the generated tree as-is, then add docs.

Commit early and often, in small working steps, with Conventional Commit
messages.
