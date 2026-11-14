# 0118. Human output by default; YAML and JSON on request

Status: Accepted · Date: 2026-11-04 · Design section: CLI and UI · Touches: 0014, 0117

Decided: `ichor` prints for a person unless `-o yaml` or `-o json` is given: a table or an aligned key/value block, enum names without their type prefix (`control-plane`, not `MACHINE_ROLE_CONTROL_PLANE`), the glyphs ✓ ! ✗ … for status and ages like `5m`, with colour only on a terminal (`NO_COLOR` and `TERM=dumb` honoured, the layout identical in a pipe). The global `-o` defaults to unset instead of `yaml`. `-o yaml` and `-o json` print exactly what they printed before, so scripts opt in by naming the format. A command with no human view yet prints YAML when `-o` is unset (`machineFormat`, used by every writer of a machine format), so nothing prints nothing; every command that reports a result has a human view now, and `ichor ui` prints its status lines in the same style; what streams or prints raw content (`logs node`, `get cni-values`, the `auth` credential plugin) stays as it is. The shared helpers are in `internal/cli/human.go`. `Cluster` and `Machine` in the Connect API gain `created_at` (for the AGE column) and `Cluster` gains `endpoint_type`, `pod_subnets`, `service_subnets` and `cni_tunnel`, so `describe cluster` can show them.

## Context

Every command dumped YAML by default, including the protobuf enum names (`MACHINE_APPROVAL_STATE_APPROVED`). `ichor init` already shows its steps as a short list with glyphs and a spinner (progress.go); the rest of the CLI should read the same way. The shared helpers have to be easy to reuse for every command.

## Decision

1. `-o` is unset by default. `validateOutputFormat` accepts unset, `yaml` and `json`. `machineFormat("")` is `yaml`; `writeMessage`, `writeOutput` and `writeOutputObjects` go through it, so an unconverted command keeps its YAML.
2. A converted command prints through `showAPIHuman` (or `printAPI`) with a render function. The render function is used only when `-o` is unset; otherwise the wire message is printed as ever. Commands whose old output was not the wire message (`get cluster` printed `key: value` lines whatever `-o` was) keep that text for `-o yaml|json`.
3. The helpers reuse progress.go's terminal detection and SGR constants: `human.Table`, `human.Block`, `human.Title`/`Line`, `human.Conditions`, `human.Mark`, `humanEnum`, `humanAge`, `humanUntil`, `conditionMark`, `shortNames`. Tables are our own column layout, not `text/tabwriter`, because tabwriter counts the bytes of a colour code as width and misaligns coloured cells. Nothing is truncated: a long cell stays whole in a pipe and on a terminal.
4. A machine's name is shortened to its first eight characters only when that stays unique in the listing; otherwise it is printed in full. `get` always prints the full name.
5. `humanEnum` strips an enum's type prefix by looking the value up in the compiled-in protobuf descriptors, so it needs no per-enum table.
6. Condition glyphs know the polarity of Ichor's condition types (`ConfigWarnings` True is `!`, `Ready` False is `✗`, `Transitioning` True is `…`).

## Consequences

- A script that read the default output must now say `-o yaml`. The CLI's own docs and guides do.
- `-o yaml|json` of `get clusters|describe cluster` and `get nodes|describe node` carry the new fields (`createdAt`, `endpointType`, `podSubnets`, `serviceSubnets`, `cniTunnel`); fields are only added, and a CLI that talks to an older ichor-ui shows `-` for an age it does not have.
- A command with a human view is a render function passed to `showAPIHuman` or `printAPI`; a start command (`upgrade`, `set kube-proxy`, `rollout config`, the node actions, `restore --live`) goes through `runStartOperation`, `runNodeAction` and `watchOperation`, which print the result and the live view.
