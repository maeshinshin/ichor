# NNNN. Title

Status: Proposed · Date: YYYY-MM-DD · Design section: x.y

Decided: one line saying what was decided, for docs/adr/README.md's index
(not "about X" -- say what X is). `internal/tools/adrindex` reads this line
to generate the index; every ADR needs one.

An amendment is marked by putting `amended` in the `Status:` value before `· Date:`, as in `Status: Accepted, amended · Date: 2026-10-26`, because `make adr-index` reads only the part of the line before `· Date:`. The amendment's own date goes on its body heading; the `Date:` field stays the original decision's date.

## Context

## Decision

## Consequences
