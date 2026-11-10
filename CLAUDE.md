# Working on Ichor

Read `docs/design/architecture.md` first; it's the source of truth. Conventions
are in `CONTRIBUTING.md`. Record a non-obvious decision in the PR description
and, if it changes the design, in the architecture doc.

Tooling for agents:

- Ponytail is installed and runs in `ultra` mode (`/ponytail ultra` if it isn't).
  Before writing code, go down its ladder: does this need to exist, does the
  standard library or an installed dependency already do it, can it be one
  line. Mark deferred shortcuts with `ponytail:` comments and run
  `/ponytail-review` on your diff before opening a PR.
- Use Serena for code navigation and editing: find symbols and references
  with its tools instead of grepping, and edit at the symbol level instead of
  rewriting files. Activate the project at the start of a session.

Rules that are easy to get wrong:

- Work inside `nix develop`. Go must be 1.27.3 or newer. Add missing tools to
  `flake.nix`; don't install them any other way.
- Scaffold with the tools (`kubebuilder`, `cobra-cli`, `npx shadcn@latest add
  @preactcn/…`), never by hand. Build and generate with the Makefile targets
  (`make help` lists them). Never call controller-gen/kustomize/envtest
  directly.
- Look up the docs for the exact library version in use (Context7 is set up
  for this) before writing code against it. Don't code from memory.
- Don't read or reference Sidero Omni — code, docs, release notes or UI. This
  is a clean-room implementation.
- Machine config is typed only (`TalosConfigPatch.spec.documents[]` with the
  Talos machinery types). No raw-YAML escape hatch.
- UI: preactcn only. No React, no `preact/compat`.
- The controller is the only writer to Talos nodes and never mutates `spec`.
  UI and CLI change the world by writing CRs.
- Ichor owns Cilium (its only CNI) as a Helm release. It does not install or
  operate Argo CD, Kokumi, Flux or any GitOps tooling, nor storage or
  monitoring.

`kubebuilder init` wants an empty directory: on a fresh repo run it first
(inside `nix develop`), commit the generated tree as-is, then add docs.

Commit early and often, in small working steps, with Conventional Commit
messages.
