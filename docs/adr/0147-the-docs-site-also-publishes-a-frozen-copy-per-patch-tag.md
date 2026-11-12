# 0147. The docs site also publishes a frozen copy per patch tag

Status: Proposed · Date: 2026-11-12 · Design section: Documentation

Decided: next to the living `/vX.Y/` of ADR 0142, the site publishes `/vX.Y.Z/` for every tag, built from that tag. It replaces the redirect pages of 0142 decision 2; a tag whose docs do not build keeps the redirect to the same page under `/vX.Y/`. The version menu lists `next`, then the minors, and under the minor being read lists that line's patches; a row shows only its name and `(latest)`, so the patch a minor documents is no longer in the label. `versions.json` gains `patches` on each line, and `/latest/` and the root still lead to the newest line.

## Context

A line's `/vX.Y/` follows its release branch, so it changes after the tag. A reader who needs the docs exactly as released, or a link that never changes, had only a redirect to the moving copy.

## Decisions

1. **The tag is the source of its copy.** `{{version}}` in `/vX.Y.Z/` is that tag, in `/vX.Y/` the newest patch of the line, in `/next/` the newest release.
2. **Existing links keep their address.** `/vX.Y.Z/<page>/` now serves the page of the tag instead of redirecting; a page the tag lacks is a 404, as before for a page the line dropped.
3. **The build grows by one Astro build and Pagefind index per tag.**

## Rejected

- Dropping the per-minor copy again: a docs fix would need a tag once more (0142).
