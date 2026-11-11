# Contributing

## Setup

```sh
nix develop
go version   # 1.27.4 or newer
make help
```

To build the CLI and the images from a checkout and run them on a Talos machine,
see [Build from source](docs/development/build-from-source.md).

All tools (Go, Node, kubebuilder, talosctl, helm, buf, goreleaser, QEMU, …)
come from `flake.nix`. If something is missing, add it there rather than
installing it some other way.

controller-gen, kustomize, setup-envtest and golangci-lint are deliberately
*not* in the devShell: the Makefile installs them into `./bin` at the
versions it pins. Don't add them to `flake.nix`.

Because those four are built and cached in `./bin`, running a Makefile target
from outside `nix develop` poisons the cache: the host toolchain builds the
binary, and `make lint` then fails with "the Go language version used to build
golangci-lint is lower than the targeted Go version" on every later run, inside
the shell or not. If you see that, `rm -rf bin` and run it again inside
`nix develop`.

Check with `./bin/golangci-lint version`: it has to say `built with go1.27.4`
(or whatever `flake.nix` pins now). If it names an older Go, the cache is
poisoned and your work was never linted, so check this before concluding that
lint failures are not yours.

## Project conventions

- The controller is a [kubebuilder](https://book.kubebuilder.io/) project.
  Add APIs and webhooks with `kubebuilder create api` / `create webhook`, and
  build with the Makefile targets (`make manifests generate test build`).
  Don't call controller-gen, kustomize or envtest by hand.
- The CLI uses cobra; add commands with `cobra-cli add`.
- **Do not run `kubebuilder edit --plugins=helm/v2-alpha`.** It regenerates
  every file under `dist/chart/templates/` from `config/`, and `dist/chart/`
  carries hand-edits the plugin knows nothing about — the webhook's fixed
  resource names, the `ValidatingAdmissionPolicy`, the UI backend's RBAC kept
  separate from the controller's, the namespaced secrets `Role` split out of
  `manager-role`, and more. Running the plugin reverted five of them in one
  pass; the next time, on an unrelated change, it reverted roughly ten —
  the whole template tree, not just the files that should have changed. A
  generator that needs most of its own output hand-restored after every run
  is not saving anyone anything. Instead:
  - **CRD schemas**: `make sync-crds` (also run by `make manifests`) copies
    each file's `spec:` block from `config/crd/bases/` into
    `dist/chart/templates/crd/`, verbatim, leaving the Helm wrapper around it
    (`{{- if .Values.crd.enabled }}`, the `{{- if .Values.crd.keep }}`
    annotation, the trailing `{{- end }}`) untouched. This one piece actually
    is mechanical — a CRD's schema has no hand-edits to lose — so it's the
    one piece still automated, as a `internal/tools/` Go command (the same pattern `internal/tools/mirrorgen` already uses for `pkg/typedconfig`'s generator), not a script in a scripting language this repo otherwise has no use for. See `internal/tools/synccrds/`.
  - **Everything else in `dist/chart/templates/`** (RBAC, the manager and
    ichor-ui `Deployment`s/`Service`s/`PodDisruptionBudget`s, the webhook
    config, the admission policy) is maintained by hand, mirroring the
    matching `config/` base when one exists. After any change that touches
    RBAC, a `Deployment`, or anything with a `config/` equivalent, render the
    chart (`helm template ichor dist/chart`) and check it did what you
    expect — the plugin's job of "look at the result" doesn't go away just
    because the plugin does.
  - **`dist/chart/chart_test.go` is the actual contract**, not a backstop:
    it renders the chart with `helm template` under two different release
    names and checks every invariant hand-maintenance must preserve, runs on
    every PR via `make test`. If you're hand-editing a template and wondering
    whether something will notice if you get it wrong, this is the thing
    that notices:
    - `checkFixedNames`: the webhook `Service`, its `ValidatingWebhookConfiguration`,
      and the cert `Secret` keep their fixed names under any release name.
    - `checkWebhookInvariants`: both webhooks keep `failurePolicy: Fail` and
      match `DELETE` only.
    - `checkKubeProxySwitchPolicy`: the kube-proxy-switch `ValidatingAdmissionPolicy`
      and its `Binding` both exist, and the `Binding` points at the right `Policy`.
    - `checkUIBackendRBACIsSeparate`: the UI backend's `Deployment` runs as its
      own `ServiceAccount`, bound only to its own `ClusterRole`, with the
      controller's own binding naming neither.
    - `checkNoCABundle`: no `caBundle` anywhere (cert-controller injects it
      at runtime; a templated one would drift on every sync).
    - `checkCertSecretHasNoData`: the webhook cert `Secret` ships metadata
      only, never a `data` key.
    - `checkSecretsRoleAndBinding`: the controller's namespaced secrets `Role`
      and its `RoleBinding` both exist and are linked.
    - `checkNoClusterWideSecretsRead`: no `ClusterRole` anywhere in the chart
      grants unrestricted `get`/`list`/`watch` on `secrets` with no
      `resourceNames` — a regression that shipped twice already.
    - `checkCRDsMatchConfig`: every CRD in `config/crd/bases/` has an
      equivalent, schema-identical CRD in the rendered chart.
    - `TestInstallerRBAC` and `TestControllerRBACHoldsNoEscalation`: the
      installer's role matches `config/rbac/ichor-installer` and never touches a
      Secret; the controller's `ClusterRole` holds no escalation, CRD or
      workload-create rights, which belong to the installer and Cilium Jobs.
  - Add a new invariant to `chart_test.go` instead of trusting memory, the
    next time a hand-edit needs protecting the way these already are.
  - **Why hand-maintenance over "run the plugin, then re-apply a patch
    script"**: a re-apply script needs its own list of what to patch back,
    maintained separately from the hand-edits themselves — today that list
    only exists in git history. Hand-
    maintaining the templates directly needs no second list: the edit and
    the file it lives in are the same thing, and `chart_test.go` is what
    would have caught the patch script going stale anyway. Fewer places to
    keep in sync wins.
  - Separately, a controller-gen trap in the CRDs it generates:
    `+kubebuilder:validation:Enum=true;false` (unquoted) emits YAML *booleans*
    into a `type: string` schema, which the API server then rejects even for
    the literal string `"true"`; quote the values instead
    (`Enum="true";"false"`).
- The UI is Vite + Preact + [preactcn](https://maeshinshin.github.io/preactcn/).
  Add components with `npx shadcn@latest add @preactcn/<name>`. No React,
  no `preact/compat`.
- The UI and CLI talk to the backend over Connect. Helm is used through its Go
  SDK.
- Check the docs for the version of a library you're using before writing
  against it.
- Read `docs/design/architecture.md` before changing behaviour.
- Never pass `paths="./..."` to controller-gen: unlike `go list ./...` it descends into
  dot directories such as `.claude/worktrees/`, and a broken nested `go.mod` there fails
  `make manifests`. Use `CONTROLLER_GEN_PATHS` in the Makefile (new top-level code roots go there).

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

- `make test` runs unit tests and envtest; `make ui-ci` runs the UI's typecheck,
  lint, node tests and build.
- `make remote-gate` runs the whole gate (sharded `go test`, `make lint`, `make
  ui-ci`) for the committed `HEAD` as parallel Jobs named `ichor-gate-*` in the
  homelab cluster's `ichor-e2e` namespace (context `admin@talos-cluster01`), so a
  small workstation is not the bottleneck. Uncommitted changes are not tested. Logs
  land in `remote-gate-out/<shard>.log`; the summary lists PASS/FAIL per shard and
  the failing tests, and the Jobs are deleted by name at the end. It needs
  `bin/k8s/1.36.2-linux-amd64` (`make setup-envtest`; `ENVTEST_SRC` points at another
  `bin/k8s`). `GATE_SHARDS="ctl:10 cli:4 uiapi:2 rest:3"`, `GATE_CPU` and `GATE_MEM`
  resize it. `GATE_ONLY="cli0 uiapi1"` re-runs just the named shards (e.g. the
  `INFRA` ones; `NOT RUN (queued)` means the cluster never scheduled the pod).
- End-to-end tests run against a Talos cluster from one of two harnesses: nodes
  on a host cluster, or `talosctl cluster create qemu` on CI runners (`make
  setup-test-e2e`). A full cluster built by `ichor init` is described in
  `test/harness/ui-demo/README.md`. The e2e harness clones one cached Talos
  ISO per version instead of importing it per node; `ICHOR_E2E_BASE_CACHE=0` turns
  that off.
- `make ui-shots` takes desktop and phone screenshots of the UI in light and dark;
  look at them before calling a UI change done.

## Cilium patch bumps

Ichor pins the Cilium chart it embeds, and a cluster's Cilium only moves to the
chart of the Ichor that runs it, so a Cilium patch reaches users in an Ichor
release.

1. `.github/workflows/cilium-patch.yml` runs daily (and from the Actions tab).
   `make bump-cilium-patch` looks for a newer final patch of the pinned minor,
   verifies the cosign signatures of its images, moves every version pin and
   regenerates the goldens. Up to date means "no newer patch" and exit 0.
2. If the bump passes `make test` and `make lint`, the workflow opens the pull
   request `chore(cni): bump the embedded Cilium chart to X.Y.Z`, linking the
   upstream release notes. A red run (an unsigned image, a changed Kubernetes
   matrix, a test that pinned the old patch) opens nothing: do the bump by hand
   from the failure. Set the repository secret `CILIUM_PATCH_PR_TOKEN` so the
   pull request also gets the Tests, Lint and PR Title checks.
3. Read the release notes and merge. This moves no cluster.
4. Cut a patch release of Ichor (push a `v*` tag, which runs the release
   workflow; `make release VERSION=vX.Y.Z` builds the same artifacts locally); users get the new chart when they upgrade Ichor and run
   `ichor cni upgrade`.

A new Cilium minor is never bumped by the script: change the pin and the values
by hand.

## Licensing

Apache-2.0, copyright maeshinshin. Files copied from Talos (MPL-2.0) keep
their license header and are listed in `NOTICE`.
