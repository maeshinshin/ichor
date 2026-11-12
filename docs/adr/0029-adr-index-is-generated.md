# 0029. The ADR index is generated from each file's own header, not hand-maintained

Status: Accepted · Date: 2026-10-26 · Design section: —

Decided: Generate `docs/adr/README.md`'s index table from a `Decided:`/`Status:` line in each ADR's own header (`internal/tools/adrindex`) instead of a shared, hand-edited table

## Context

An index table kept by hand in one shared file is a guaranteed merge conflict
for every contribution that adds an ADR: each of those PRs touches the same
lines of `docs/adr/README.md`. Two contributors working in parallel each
adding a row at the bottom of the file collide textually even when their rows
do not overlap, and a conflict resolved carelessly can duplicate or drop a
row. A test that checks the table against the files on disk catches staleness
after the fact, but it cannot prevent the conflict itself.

## Decision

Each ADR file carries a `Decided: <one-line summary>` line immediately after
its `Status: ... · Date: ... · Design section: ...` line.
`internal/tools/adrindex` (following `internal/tools/synccrds`'s
command-per-generated-artifact pattern) reads every `docs/adr/NNNN-*.md` file,
pulls its `Decided:` text and the first clause of its `Status:` line, and
overwrites `docs/adr/README.md`'s table with the result, sorted by number.
`make adr-index` runs it; the preamble prose above the table is a constant in
the generator, not read back from the file, so the whole output is
reproducible from the ADR files alone.

This moves the one file every contributor shared into N independent files,
each owned by whoever wrote that ADR. Contributors adding ADRs in the same
window touch disjoint files, `NNNN-title.md` and `MMMM-title.md`, and
`README.md` is never hand-edited, so there is nothing left to conflict on.

The hand-maintained-table checks (a test that the table matches the files, and
one that the numbers are contiguous) are not kept alongside the generator.
Both exist to catch a hand-maintained table drifting from the files on disk;
once the table is generated from those same files, that drift is impossible by
construction. `internal/tools/adrindex`'s own `TestGenerateIndexIsCurrent`
is the CI check that regenerating changes nothing, the same drift-detection
shape `dist/chart`'s CRD sync uses. A test that can no longer fail for the
reason it was written is deleted rather than kept as a second implementation
of the same check.

The generator has no source for an ADR's status other than the header line, so
an ADR that records an amendment must say "amended" in its `Status:` line, not
only in a later section.

## Consequences

- `0000-template.md` documents the `Decided:` line for new ADRs; an ADR
  without one fails `adrindex` loudly, and `TestGenerateIndexIsCurrent`
  catches it the same way a missing chart file would.
- An ADR's `## Context`/`## Decision`/`## Consequences` content is independent
  of the index; only the header lines feed it.
