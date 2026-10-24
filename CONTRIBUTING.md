# Contributing

## Setup

```sh
nix develop
go version   # 1.27.3 or newer
make help
```

All tools (Go, Node, kubebuilder, talosctl, helm, buf, goreleaser, QEMU, …)
come from `flake.nix`. If something is missing, add it there rather than
installing it some other way.

controller-gen, kustomize, setup-envtest and golangci-lint are deliberately
*not* in the devShell: the Makefile installs them into `./bin` at the
versions it pins. Don't add them to `flake.nix` — see
[ADR 0005](docs/adr/0005-build-tools-come-from-the-makefile.md).

Because those four are built and cached in `./bin`, running a Makefile target
from outside `nix develop` poisons the cache: the host toolchain builds the
binary, and `make lint` then fails with "the Go language version used to build
golangci-lint is lower than the targeted Go version" on every later run, inside
the shell or not. If you see that, `rm -rf bin` and run it again inside
`nix develop`.

## Project conventions

- The controller is a [kubebuilder](https://book.kubebuilder.io/) project.
  Add APIs and webhooks with `kubebuilder create api` / `create webhook`, and
  build with the Makefile targets (`make manifests generate test build`).
  Don't call controller-gen, kustomize or envtest by hand.
- The CLI uses cobra; add commands with `cobra-cli add`.
- The Helm chart in `dist/chart` is generated from `config/` by
  `kubebuilder edit --plugins=helm/v2-alpha`; everything under
  `dist/chart/templates/` is overwritten on every run. One trap: controller-gen
  names the `ClusterRole` and the namespaced `Role` it generates both
  `manager-role`, and the Helm plugin can only convert one of them under that
  shared name — so a new RBAC rule in the wrong scope is silently dropped from
  the chart while kustomize still renders it. After any RBAC change, render the
  chart and check the rules are actually there. Several `templates/` files
  carry hand-edits for exactly this kind of thing that regeneration would
  revert with no other signal; `dist/chart/chart_test.go` renders the chart
  with `helm template` and guards those edits, runs on every PR via `make
  test`.
- The UI is Vite + Preact + [preactcn](https://maeshinshin.github.io/preactcn/).
  Add components with `npx shadcn@latest add @preactcn/<name>`. No React,
  no `preact/compat`.
- The UI and CLI talk to the backend over Connect. Helm is used through its Go
  SDK.
- Check the docs for the version of a library you're using before writing
  against it.
- Read `docs/design/architecture.md` before changing behaviour. Decisions that
  the design leaves open go in `docs/adr/`.

## Commits and pull requests

- Commit as you go. A commit should be one logical step that builds and passes
  tests: a scaffold, a type, a reconciler, a test. Don't batch a day's work
  into one commit, and don't commit broken intermediate states.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for commit
  messages and PR titles (`feat:`, `fix:`, `docs:`, `test:`, `chore:`,
  `refactor:`, `build:`, `ci:`). The changelog is generated from them, and CI
  checks PR titles against this list.
- One PR per task. `main` is protected; CI (lint, unit tests, a short e2e run)
  has to pass.
- Say in the PR description which docs you consulted and which version.

## Tests

- `make test` runs unit tests and envtest.
- End-to-end tests create throwaway clusters with `talosctl cluster create`
  (QEMU). The short set runs on every PR; the full set runs nightly.

## Licensing

Apache-2.0, copyright maeshinshin. Files copied from Talos (MPL-2.0) keep
their license header and are listed in `NOTICE`.
