# 0079. Node logs start from the tail and the viewer is bounded

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI

Decided: `StreamNodeLogs` without a resume token starts from the last `tail_lines` lines (default 500, cap 10000, negative for the whole history) instead of the beginning of the log; `follow=false` returns that snapshot and ends; the UI viewer keeps the newest 2000 lines, appends in 100 ms batches, shows at most 1000 characters of a message until expanded, follows the tail only while scrolled to the bottom, can pause, backs off between reconnects and shows stream errors inline.

## Context

The proto said "omit resume_token to start from the current tail" but `StreamLogs` called Talos `Logs` with `tailLines = -1` (everything), so a kubelet stream began with its startup dump and replayed the whole history. The page then did one `setLines` (copying a 500-element array) and one `scrollIntoView` per received line, and `streamWithResume` reconnected on `Unavailable` with no delay and no limit, so an unreachable node produced a hot request/re-render loop. Opening the logs froze or killed the tab, worst on a phone.

## Decision

1. `StreamNodeLogsRequest.tail_lines` (0 = 500, above 10000 = 10000, negative = all) and `optional bool follow` (unset = true, so existing callers are unchanged). `NodeTalosClient.StreamLogs` takes a `LogOptions{Follow, TailLines}`. Service logs pass both to Talos `Logs`. The kernel log (`Dmesg`) has only a boolean tail, so a snapshot reads the ring and keeps the last `tail_lines` lines in the backend; a followed kernel stream keeps the boolean tail as before.
2. Resume still replays from the tail window, not from the token's exact position: lines are stamped with the time the backend sees them, so a resumed stream re-sends the window and the token only filters by that stamp. Bounded by the tail, so accepted for now.
3. The viewer's numbers (2000 lines, 100 ms, 1000 characters) are constants in `NodeLogs.tsx`; 2000 rows of capped text is cheap on a phone.
4. `streamWithResume` waits 500 ms doubling to 10 s between reconnects, gives up after 8 in a row with no message, and stops when its signal is aborted.

## Consequences

The log viewer cannot grow without bound, and a bad node ends in an error the person can read. The 500-line default means history older than that is not shown; a caller wanting more passes `tail_lines`.
