# 0145. The docs take the release number from the build

Status: Accepted · Date: 2026-11-11 · Design section: Documentation

Decided: a docs page writes the release it tells a reader to install as `{{version}}` (no leading `v`), and the site build replaces it, in the page, its `.md` copy and `llms*.txt`. A `/vX.Y/` copy shows the patch it documents (`DOCS_RELEASE`, set by `build-all.mjs`); `/next/` shows the newest `vX.Y.Z` tag, never an unreleased number. Prose does not state a version. `README.md` is not built by the site, so it keeps literals and a Go test (`internal/tools/readmeversion`) fails when they are older than the newest tag.

## Context

The v0.2.0 docs still said v0.1.0: install snippets and status lines were edited by hand at each release and one was missed.

## Rejected

- A templating dependency: one `replaceAll` in the remark pipeline does it.
- Substituting in README: GitHub renders it as written; a check is enough.
- Requiring README to equal the newest tag: the bump could not land before the tag, so the check allows it to be ahead.

## Consequence

On GitHub a page shows `{{version}}` literally. A docs-only fix on `release-X.Y` or a tag republishes the line, so the published `/vX.Y/` is fixed once the line is rebuilt from a ref that contains this change.
