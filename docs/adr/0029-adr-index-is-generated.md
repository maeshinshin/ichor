# 0029. The ADR index is generated from each file's own header, not hand-maintained

Status: Accepted · Date: 2026-10-26 · Design section: —

Decided: Generate `docs/adr/README.md`'s index table from a `Decided:`/`Status:` line in each ADR's own header (`internal/tools/adrindex`) instead of a shared, hand-edited table

## Context

Every agent working on a different unit adds its own ADR, and every one of
those PRs also touched the same two lines of `docs/adr/README.md` -- the
table's header row's neighbor. With agents landing in parallel, this file
conflicted five times in a row; one of those conflicts was resolved wrong
and briefly duplicated a row. `index_test.go`'s `TestIndexMatchesFiles`
caught staleness after the fact, but it could not prevent the conflict
itself: two agents each adding a row at the bottom of the same file still
collide textually even when their rows don't overlap.

## Decision

Each ADR file now carries a `Decided: <one-line summary>` line immediately
after its existing `Status: ... · Date: ... · Design section: ...` line
(the line README.md's table already carried, moved rather than duplicated).
`internal/tools/adrindex` (new, mirroring `internal/tools/synccrds`'s
existing command-per-generated-artifact pattern) reads every
`docs/adr/NNNN-*.md` file, pulls its `Decided:` text and the first clause
of its `Status:` line, and overwrites `docs/adr/README.md`'s table with the
result, sorted by number. `make adr-index` runs it; the preamble prose above
the table is a constant in the generator, not read back from the file, so
the whole output is reproducible from the ADR files alone.

This moves the only file every agent used to share into N independent
files, each owned by whoever wrote that ADR. Two agents adding ADRs in the
same window now touch disjoint files -- `NNNN-title.md` and
`MMMM-title.md` -- and `README.md` is never hand-edited at all, so there is
nothing left for them to conflict on.

`docs/adr/index_test.go` (`TestIndexMatchesFiles`, and `TestNumbersAreContiguous`
before it) is deleted, not kept alongside the generator. Both tests existed
to catch a hand-maintained table drifting from the files on disk; once the
table is generated from those same files, that drift is no longer
possible by construction, and `internal/tools/adrindex`'s own
`TestGenerateIndexIsCurrent` (`go test ./internal/tools/adrindex/...`)
already is the CI check that regenerating changes nothing -- the same
drift-detection shape `dist/chart`'s CRD sync already uses. A test that
can no longer fail for the reason it was written is deleted, not kept as
a second implementation of the same check (deletion beats
addition).

Three ADRs (0002, 0018, 0020, 0021) had a `Status:` header that did not
say "amended" even though README.md's table did -- the amendment was
recorded in an `## Amendment` (or, for 0020, `## Resolved gap`) section
instead of in the header. Their `Status:` lines were corrected to match
what README.md already recorded, since the generator has no source for
that fact other than the header line itself.

## Consequences

- `0000-template.md` now documents the `Decided:` line for new ADRs; an
  ADR without one fails `adrindex` loudly (`TestGenerateIndexIsCurrent`
  catches it the same way a missing chart file would).
- Not built here: any change to an ADR's `## Context`/`## Decision`/
  `## Consequences` content -- only the header line each one already had
  moved, and the `Status:` line correction above.
