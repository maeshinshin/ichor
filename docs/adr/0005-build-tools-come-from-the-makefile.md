# 0005. controller-gen, kustomize, setup-envtest and golangci-lint come from the Makefile

Status: Accepted · Date: 2026-10-25 · Design section: 0.2, 0.3 · Amends ADR 0002

Decided: Moved controller-gen, kustomize, setup-envtest and golangci-lint out of `flake.nix` and into Makefile-managed `./bin`

## Context

ADR 0002 put all four of kubebuilder's build tools in the devShell and
overrode `CONTROLLER_GEN`, `KUSTOMIZE`, `ENVTEST` and `GOLANGCI_LINT` with Nix
store paths, so that the generated Makefile would use the flake's binaries and
still not need editing. The reasoning was sound and the mechanism (`?=`) is
real, but the approach cannot work:

- The rule is `$(CONTROLLER_GEN): $(LOCALBIN)`, with `bin/` as a prerequisite.
  `bin/` is newer than any store path, so Make always runs the recipe.
- The recipe is `go-install-tool`, whose first act is `rm -f "$(1)"`. When
  `$(1)` is a store path that is `rm: cannot remove '/nix/store/…': Read-only
  file system`, and `set -e` aborts. Every one of `make build`, `make test`,
  `make manifests` and `make lint` dies there, before reaching the tool.
- Pre-creating `bin/<tool>-<version>` symlinks to satisfy the macro's
  freshness check doesn't survive a second run either: the macro ends with
  `ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"`, which repoints `bin/<tool>` at
  the resolved store path, so the next run's `readlink` comparison fails and it
  tries to reinstall.
- `golangci-lint` settles it. Its target, when `.custom-gcl.yml` exists — and
  `kubebuilder init` scaffolds one — runs `golangci-lint custom --destination
  $(LOCALBIN)` to *build* a plugin-carrying binary and then `mv`s it over the
  tool path. A read-only store binary can never serve that purpose.

Meanwhile `CONTRIBUTING.md` says to never call controller-gen, kustomize or envtest
directly, and to build and generate through the Makefile targets. Tools nobody
is allowed to invoke directly don't need to be on `PATH`; putting them there
mostly creates the temptation the rule forbids.

## Decision

Let the generated Makefile own its own build tools, and take them out of the
devShell: drop `kubernetes-controller-tools`, `kustomize`, `setup-envtest` and
`golangci-lint` from `flake.nix`, along with the four environment overrides.
`make` installs them into `./bin` at the versions it pins
(`CONTROLLER_TOOLS_VERSION`, `KUSTOMIZE_VERSION`, `GOLANGCI_LINT_VERSION`, and
`ENVTEST_VERSION` derived from `go.mod`), which is how kubebuilder intends its
Makefile to work.

The devShell keeps what contributors actually type: Go, kubebuilder,
cobra-cli, talosctl, kubectl, helm, buf and the protoc plugins, goreleaser,
govulncheck, QEMU, cni-plugins, Node, make, git.

## Consequences

- `make build`, `make test`, `make manifests` and `make lint` work as
  kubebuilder shipped them, and `kubebuilder alpha update` keeps working
  because the Makefile stays unedited apart from the `GOLANGCI_LINT ?=`
  one-character fix (now unnecessary, but harmless and left in place).
- The versions of these four tools are the ones kubebuilder 4.15.0 targets, rather than nixpkgs' newer ones. For build tooling that is the safer
  side to be on.
- This weakens the letter of the rule that every tool comes from `flake.nix`:
  these four come from the Go module proxy instead. It keeps the rule's intent
  — one toolchain for contributors and CI, pinned, with no ad-hoc or global
  installs — because the versions are pinned in the Makefile and resolved the
  same way everywhere. The rule still holds for everything else.
- `make` needs the module proxy the first time it runs in a fresh checkout, on
  top of the network it already needs for `go mod download` and for envtest's
  assets. An offline build would have to populate `./bin` and the envtest
  cache beforehand; see the `shortcut:` note in `flake.nix`.
