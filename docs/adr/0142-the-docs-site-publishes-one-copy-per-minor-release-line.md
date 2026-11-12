# 0142. The docs site publishes one copy per minor release line

Status: Accepted, in part superseded by ADR 0147 (the redirect pages and the version menu) · Date: 2026-11-11 · Design section: Documentation

Decided: the docs site has `/vX.Y/` per minor line instead of `/vX.Y.Z/` per tag. It is built from the branch `release-X.Y` when that exists, else from the newest tag `vX.Y.*`. `/next/` stays main, and `/latest/` and the site root lead to the newest line. Every published tag path `/vX.Y.Z/<page>/` redirects to `/vX.Y/<page>/`. The version menu lists lines with the patch they document (`v0.1 (v0.1.1)`); `versions.json` gains a `patch` field.

## Context

A docs fix reached readers only with a new tag, so docs-only patch releases were cut for that reason. Docusaurus, Tyk and Replicated publish docs per minor line.

## Decisions

1. **The branch is the source, the tag the fallback.** A push to `release-*` triggers the docs workflow, so a fix needs no tag; before the branch exists nothing changes for readers.
2. **Old links keep working.** The same small redirect page the site root uses is written for every page of the line under each of its tags. A page the line dropped since that tag is a 404, the overview always redirects.
3. **The Helm repository is untouched.** `/charts/` is still built from every tag.

## Rejected

- A copy per patch plus a fix branch: the cost this removes.
- Building the docs from `main` for released lines: releases would describe unreleased behaviour.
