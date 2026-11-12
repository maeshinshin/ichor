# 0002. Nix devShell and Go pin

Status: Accepted, amended · Date: 2026-10-24 · Design section: —

Decided: Pinned `nixpkgs` to `nixpkgs-unstable` and committed `flake.lock` for a reproducible shell

## Context

Every contributor and CI job needs to run inside a reproducible shell with the whole
project toolchain (Go, kubebuilder, controller-gen, kustomize, setup-envtest,
cobra-cli, talosctl, kubectl, helm, buf/protoc-gen-go/protoc-gen-connect-go,
goreleaser, golangci-lint, govulncheck, QEMU + cni-plugins, Node, make). The
toolchain list is `CONTRIBUTING.md`'s Setup section. Go has to be 1.27.3 or
newer.

On `nixpkgs-unstable` the default `go` attribute is 1.26.9; Go 1.27.3 is
reachable only through the `go_1_27` attribute. Since 1.27 is the current Go
release series, no stable NixOS release channel carries it yet; only
`nixpkgs-unstable` does.

## Decision

- Pin the flake's `nixpkgs` input to `github:NixOS/nixpkgs/nixpkgs-unstable`
  and commit `flake.lock`, so the shell is reproducible even though the
  channel itself isn't stable.
- Use the `go_1_27` attribute explicitly in the devShell's `packages`, not the
  plain `go` attribute, since `go` currently resolves to 1.26.9.
- Export `CONTROLLER_GEN`, `KUSTOMIZE`, `ENVTEST` and `GOLANGCI_LINT` as
  absolute Nix store paths (`${pkgs.kubernetes-controller-tools}/bin/controller-gen`,
  etc.) from the devShell. Kubebuilder's generated Makefile declares these
  variables with `?=` and otherwise `go install`s its own copies into
  `./bin`; setting them as shell env vars lets the Makefile stay unedited
  while the binaries still come from the flake, per `CONTRIBUTING.md`'s rule
  against calling controller-gen/kustomize/envtest by hand.
- Define `devShells.<system>.default` for `x86_64-linux` and `aarch64-linux`
  via `nixpkgs.lib.genAttrs`, with `nixpkgs` as the only flake input.

## Consequences

- `nix develop` gives every tool on PATH at the pinned versions, and
  `nix flake check`/`flake.lock` make the shell reproducible across machines.
- Tracking `nixpkgs-unstable` means the pin needs to be bumped deliberately
  (`nix flake update`) rather than drifting with a stable channel; revisit
  once Go 1.27.3 lands in a stable NixOS release and switch back if wanted.
- Known gap: `setup-envtest`'s kube-apiserver/etcd *assets* are not packaged
  in nixpkgs, so `setup-envtest` still downloads them from the network the
  first time `make test` runs envtest. Marked in `flake.nix` with a
  `shortcut:` comment. If an offline/air-gapped CI is ever needed, this is
  the thing to fix.

## Amendment, 2026-10-25

Two things turned out differently once the kubebuilder project existed.

**The four tool overrides don't work and the tools are gone from the
devShell.** The generated Makefile's `go-install-tool` macro starts by
`rm -f`-ing the path it is pointed at, which can never succeed against a Nix
store path, and its `golangci-lint` target *builds* a plugin-carrying binary
rather than running a fixed one. ADR 0005 supersedes that part of this
decision: `controller-gen`, `kustomize`, `setup-envtest` and `golangci-lint`
come from the Makefile, not from the flake.

**`go.mod` carries the `go` directive alone, with no `toolchain` line.** A
`toolchain` line equal to the `go` directive is redundant, so `go mod tidy`
strips it, and restoring it by hand makes `go build` and `go vet` refuse to run
until the module is tidied again. The two cannot coexist. `go 1.27.3` on its
own is what pins the floor — an older toolchain either upgrades itself or
refuses — and CI checks the version as well.
