# 0071. The machine list shows the Ready condition, not Talos's stage

Status: Accepted · Date: 2026-10-26 · Design section: UI
Decided: The machine list drops its "Stage" column. `Machine.stage` is only filled by the node view (`GetNode`), which dials the machine; the list reads CRs only. The approved list shows the TalosMachine `Ready` condition (status and reason) instead; the pending list shows no status column.

## Context

The machine list's Stage column always showed "-". Root cause: `machineToPB` sets `Stage: ""` on purpose (ADR 0018 keeps Talos's own `MachineStatus` stage a `GetNode` concern), the controller records no stage in `TalosMachine.status`, and the list never calls `GetNode`. So the column could never have a value. A pending machine in maintenance mode has no credentials to dial, and a stage per row would mean one Talos call per row on every list load.

## Decision

Keep `Machine.stage` in the proto for the node view, remove the always-empty column, and show what the controller does record: the `Ready` condition. The Talos stage belongs on the machine page's Talos details, where one node is dialled.

## Consequences

- No new controller status field and no per-row Talos call on the list.
- A control plane that is approved but not Ready shows `False` / its reason (for example `RebootRequired`) in the list.
