# 0017. UI skeleton: ui/, ichor-ui as a separate binary, building without it

Status: Accepted · Date: 2026-10-25 · Design section: CLI and UI

Decided: Settled `ui/`'s directory, `cmd/ichor-ui` as a second binary, and how the Go side builds without the UI present

## Context

Phase 5 is the UI skeleton only: scaffolding, not screens. The design
(`docs/design/architecture.md`, "CLI and UI" and "Overview") already settles
several things that would otherwise need deciding here: the frontend is "a
Vite + Preact + TypeScript SPA built with preactcn, embedded in the Go
backend"; the backend is a named deliverable, `ichor-ui`, "a Go backend that
embeds the Preact SPA. It reads and writes CRs, reads logs and state from the
Talos API, and never writes to nodes"; and "the CLI and the UI backend are
added beside [`cmd/main.go`]". What was left open: the frontend directory
name, where `ichor-ui`'s source lives, how `npx shadcn@latest add
@preactcn/...` is actually wired up for a non-React project, which preactcn
components to install for a skeleton, and — the one with teeth — how to keep
`go build ./...`/`go vet ./...`/golangci-lint working for anyone who never
touches the UI, given the Go side embeds its output.

Constraints already fixed by the project's conventions: preactcn only, no React, no
`preact/compat`; components come from the shadcn CLI against the `@preactcn`
registry, never hand-written; the browser talks only to the backend, never to
a third-party host directly.

## Decision

**Directory and binary.** The frontend lives at `ui/` (plain, matches the
design's "the UI" with no other candidate name in play). The backend is
`cmd/ichor-ui/`, a second `main` package beside the controller's
`cmd/main.go`, per the design's explicit "added beside it" and the `ichor-ui`
name it already assigns. It does not import `pkg/`, `internal/` or `api/`
yet — this phase has no Connect API client and no CR reads/writes to wire up,
so there is nothing shared to pull in. It imports nothing from `cmd/` either,
matching "deliverables don't import each other."

**Scaffolding, done with the CLIs the rules require.**
- `npm create vite@latest ui -- --template preact-ts` (create-vite 9.3.1) —
  Preact 10.29.8, Vite 8.3.4, zero React.
- preactcn (https://github.com/maeshinshin/preactcn) ships no Vite/Preact
  `shadcn init` template — there isn't one upstream for Preact — so its own
  README spells out the equivalent by hand: install `tailwindcss
  @tailwindcss/vite tw-animate-css shadcn`, add the Tailwind Vite plugin and a
  `@/*` path alias in `vite.config.ts`/`tsconfig*.json`, and write
  `components.json` pointing `registries."@preactcn"` at
  `https://maeshinshin.github.io/preactcn/r/{name}.json`. That
  `components.json` is configuration, not a component, so writing it by hand
  from preactcn's own documented recipe doesn't violate "scaffold with the
  tools, never by hand" — there is no tool for it.
- Components: `npx shadcn@latest add @preactcn/button @preactcn/card
  @preactcn/separator` (shadcn CLI 4.22.1). Three are enough for a skeleton: `Button` and `Separator` build the nav shell, `Card` is the
  page container every placeholder screen uses. The CLI wrote
  `src/components/ui/{button,card,separator}.tsx` plus the Radix-for-Preact
  interop it depends on under `src/lib/radix/` and `src/lib/utils.ts` — all
  generated, all Preact, no `react` or `react-dom` anywhere in the dependency tree.
- The shadcn `add` step does not also write Tailwind's `:root`/`.dark`
  variable block (there is no Preact `init` to do it, and `add` only touches
  component files) — without it, the components' `bg-primary`/`border-border`
  etc. classes exist in markup but resolve to nothing. The `shadcn` npm
  package exports its shared utility layer as `shadcn/tailwind.css`
  (per the package's `exports` map), and
  ui.shadcn.com/docs/theming publishes the standard oklch `:root`/`.dark`
  variable block for Tailwind v4. `src/index.css` imports the former and
  defines the latter; both are checked in since generating them isn't a
  component-install step either.

**Building without the UI.** `cmd/ichor-ui` has no screens to speak of yet,
but it still has to compile for anyone who only runs `go build`/`go
vet`/golangci-lint, before `ui/` is ever touched. Two separate problems, two
separate fixes:
- `embed.FS` needs *something* to embed at compile time. `webfs_dev.go`
  (`//go:build !uiembed`, the default) embeds a committed
  `cmd/ichor-ui/placeholder/index.html`. `webfs_embed.go` (`//go:build
  uiembed`) embeds `cmd/ichor-ui/dist`, which only exists after `make
  ui-build` — Vite's `build.outDir` points straight at it, no copy step.
  Because the two files carry mutually exclusive build tags, only one
  `//go:embed` directive is ever evaluated, so the missing `dist/` directory
  is never a problem for the default build. `make build-ichor-ui` runs
  `ui-build` then `go build -tags uiembed`.
- Separately: `go build ./...`, `go vet ./...` and `go test ./...` walk the
  whole module by directory, and npm dependencies are not obliged to contain
  only JavaScript — `ui/node_modules/flatted` vendors a Go port of itself,
  which `go test ./...` and `golangci-lint run` would otherwise walk into
  (`forbid-sort-pkg` trips inside it). `ui/go.mod` gives `ui/` its own module
  boundary, which is enough for `./...` from the repo root to skip it
  entirely, with no `.golangci.yml` change: golangci-lint resolves packages
  the same way `go list` does. An explicit `^ui/` path exclusion in
  `.golangci.yml` adds nothing once the module boundary is in place, so there
  is none.

**Serving.** `cmd/ichor-ui/main.go` is deliberately small: one
`http.FileServerFS` over whichever `fs.FS` the build tag selected, wrapped so
a path that doesn't exist in that FS is rewritten to `/` and re-served —
the standard SPA fallback, so client-side routes 200 instead of 404. A
`-addr` flag and `signal.NotifyContext`/`srv.Shutdown` round it out. No
health endpoint, no metrics, no streaming: none of that exists to serve yet.

**App shell.** `src/router.tsx` is a ~30-line History-API router (`usePath`,
`navigate`, `Link`) rather than a dependency — `preact-router` or similar
would be the obvious reach, but a route table with one page component
doesn't need one.
`src/pages/Placeholder.tsx` is the one page component every route renders
("Coming soon" in a `Card`); `src/app.tsx` wires two routes (`/`, `/gitops`)
through it with `Button`/`Separator` for the nav, standing in for the real
nav this skeleton doesn't build.

## Consequences

- Adding a fourth screen later is "add a route table entry and a page
  component," not a framework change.
- `ui/go.mod` means `ui/` packages (there are none, and shouldn't be any)
  would need an explicit `GOWORK`/replace to be imported from the main
  module — moot today since nothing does, or should.
- The base Tailwind theme is the stock shadcn neutral/oklch palette, picked
  for being well-documented and exactly reproducible, not reviewed for
  Ichor's actual visual identity. Expected to be revisited once there's a
  real screen to look at.
- `spaHandler` takes an `fs.FS` and does not depend on either `embed.FS`
  variant: an existing path is served as-is, an unknown path falls back to
  `index.html` with `200`, the same under the default build and
  `-tags uiembed`.
- There is currently no path that ships `ichor-ui` anywhere. `-tags uiembed`
  is wired into exactly one place, `make build-ichor-ui`; `.goreleaser.yaml`
  builds only `./cmd/ichor`, and no container image or chart `Deployment`
  references `ichor-ui` at all. That's correct for a skeleton phase — there's
  nothing worth shipping yet — but it means today's repo can produce a
  `bin/manager` and a CLI release without ever touching the UI, and a
  `build-ichor-ui` run that silently used the default tag would ship the
  placeholder page instead of an error. The phase that actually ships the UI
  needs to add `ichor-ui` to goreleaser, a container image, and a
  `Deployment` in the chart (with the two-replica/PDB/anti-affinity shape
  "CLI and UI" describes), and should make building it the wrong way loud
  rather than silent.

## Alternatives considered

**Running `npx shadcn@latest init -t vite`.** Targets React's Vite template;
it would scaffold React-flavored files and fight preactcn's own documented
manual setup rather than complementing it. Its `-t`/`-b` options offer no
Preact choice.

**Copying `ui/dist` into `cmd/ichor-ui/dist` as a build step.** Works, but is
one more moving part than pointing Vite's `outDir` at the destination
directly.

**One binary for the controller and the UI backend.** Rejected by the design
itself (`ichor-controller`, `ichor-ui` and `ichor` are three deliverables);
also wrong on trust grounds — "only the controller holds an
`os:admin`-equivalent Talos identity; the UI backend holds a read-only one."

**A `.golangci.yml` path exclusion instead of (or alongside) `ui/go.mod`.**
Redundant once the module boundary exists, since golangci-lint's package
discovery follows the same module rules `go list` does. The module boundary is
one new file (`ui/go.mod`); the exclusion would be a second entry in a shared
config file that does nothing.
