# 0030. `ichor ui`: find the pod through its Service, reforward on failure

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI

Decided: `ichor ui` discovers ichor-ui pods through the `ichor-ui-service` Service's own selector/port and reforwards to another ready pod (same local port) whenever the tunnel ends, instead of targeting one pod name

## Context

architecture.md's "CLI and UI" section is explicit: "In the first release the
UI is reached through `ichor ui` (a port-forward) ... `ichor ui`
re-establishes its port-forward to another pod when the current one goes
away." `cmd/ichor-ui` (the server) and `internal/uiapi` (its handlers) already
exist; nothing about reaching it from the CLI did until this unit. The chart
(`dist/chart/values.yaml`'s `ichorUI.replicas: 2`) assumes more than one pod
exists specifically so a drain or a rollout never takes the UI down -- `ichor
ui` has to actually survive that, not just document that it should.

## Decision: discover pods through the Service, not a fixed label guess

`runUI` (`internal/cli/ui_run.go`) first `Get`s the fixed-name
`ichor-ui-service` Service (`dist/chart/templates/ui/ui-service.yaml`'s own
comment: "Fixed name, not release-templated ... a future `ichor ui`
port-forward ... need[s] a predictable name") and uses *its* `.spec.selector`
and `.spec.ports[0].port` to list pods and pick a target port. The
Deployment's pod-template labels are release-templated
(`include "ichor.name" .`) and so not safe to hardcode; the Service's own
selector is already whatever that template resolved to, so reading it back
avoids guessing a label value this command has no other way to know.

## Decision: a reforward loop, not a single `kubectl port-forward` call

`selectPod` (pure, `internal/cli/ui_run.go`) is the actual decision: among
ready pods (`Running`, `PodReady` condition `True`, no
`deletionTimestamp`), prefer one other than the pod that just failed,
falling back to it if it is the only ready pod there is (the
`replicas: 1` case). `runUI` wraps this in a loop: list pods, forward to
whichever `selectPod` returns, and when that `Forward` call ends for any
reason (an error, or a clean return with the context not yet cancelled --
both look the same from a lost SPDY stream, e.g. the pod being deleted
mid-drain), print what happened and go around again, excluding that pod's
name. The local port never changes across a reforward, so a browser tab
already pointed at `localhost:<port>` keeps working without the user doing
anything.

Each iteration prints to stderr before and after: "forwarding
localhost:N -> pod/X" (first attempt) or "re-established the port-forward
-> pod/X" (every attempt after), and "lost the port-forward to pod/X
(err); looking for another pod..." when it ends. This is the design's
"does not break" requirement applied to a CLI rather than a controller:
nothing here blocks silently, and a user watching the terminal can always
tell whether `ichor ui` is still working or has found a problem.

## Decision: the real tunnel is client-go's own `tools/portforward`, swapped out in tests

`ui_portforward.go`'s `portForwardToPod` is the one place that touches a
real cluster: `transport/spdy.RoundTripperFor` plus
`tools/portforward.NewOnAddresses`, the same two client-go packages
`kubectl port-forward` itself is built on (not reimplemented --
an already-present dependency is used rather than rewritten). `uiDeps.Forward` is the
seam: `runUI`'s own tests (`internal/cli/ui_run_test.go`) pass a fake that
never opens a socket, only drives the ready/error/context-done paths
`selectPod`'s decision depends on -- proving the reforward behavior and
the user-visible messages without a real cluster, the same
dependency-injection shape `initDeps`/`adoptDeps` already use for Talos
and Kubernetes calls elsewhere in this package.

## Decision: open the browser with the dependency already in the module graph

`k8s.io/kubectl` already pulls in `github.com/pkg/browser` indirectly;
`ui_browser.go`'s `openBrowser` calls `browser.OpenURL` directly rather than
hand-rolling the usual `xdg-open`/`open`/`rundll32` OS switch, promoting it
from an indirect to a direct `require` in `go.mod` (also pulling
`github.com/gorilla/websocket` and `github.com/moby/spdystream` into
`go.sum`, both already-transitive dependencies of `k8s.io/client-go`'s
`tools/portforward`/`transport/spdy` packages this unit imports directly for
the first time).

## Consequences

- `ichor ui` authenticates as whoever runs it, through `--kubeconfig`, the
  same as every other `ichor` subcommand -- no ServiceAccount or RBAC is
  added by this unit; `pods/portforward`, `pods` get/list and `services`
  get on the ichor-ui namespace are the cluster operator's own permissions
  to grant, same as `kubectl port-forward` needs.
- Not built here: anything in `cmd/ichor-ui` or `internal/uiapi` (read only,
  per this unit's own instructions) -- `ichor ui` only ever reaches as far
  as the TCP tunnel; what runs over it is already built.
