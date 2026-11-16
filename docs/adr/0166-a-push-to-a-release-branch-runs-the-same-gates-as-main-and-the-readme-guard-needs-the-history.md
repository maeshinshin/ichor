# 0166. A push to a release branch runs the same gates as main, and the README guard needs the history

Status: Accepted · Date: 2026-11-16 · Design section: Deployment · Relates to: 0108, 0145

Decided: `test.yml` and `lint.yml` run on a push to `release-*` as well as to `main`, because patches are cherry-picked from main onto the release line and arrive outside any tag event. Every job that runs `make test` (`test.yml`, `cilium-patch.yml` and the release workflow's `verify`) checks out with `fetch-depth: 0`. The README version guard (`internal/tools/readmeversion`) compares a release branch with the tags it descends from and main with all tags, and in CI it fails, rather than skips, when the tags or their history are missing.

## Context

ADR 0108 gives the release workflow's `verify` job the same checks as a pull request (module drift, lint, tests, UI CI, govulncheck, npm audit). That gates a tag. A commit pushed to `release-0.1` or `release-0.2` is not a tag: `test.yml` and `lint.yml` were `branches: [main]`, so a direct push to a release branch ran neither, and the first gate it met was the tag, after the commit was already on the line.

The README guard skipped itself when `git tag` listed no release tag. A tag push checks out that one tag, and a default branch checkout has no tags, so in those checkouts the guard asserted nothing. `test.yml` had asked for the tags with `fetch-tags: true`; `cilium-patch.yml` and the release workflow's `verify` had not.

Fetching every tag makes a second problem visible. The guard compared the README with the newest `vX.Y.Z` tag of the repository. `release-0.1` names 0.1.x in its README and the repository holds `v0.2.x`, so on that branch the guard would always fail, on a branch push and on a `v0.1.x` tag.

## Decision

- **`test.yml` and `lint.yml` also run on a push to `release-*`.** The repository's rule is that a patch is cherry-picked from main onto the release line, so those branches receive commits outside any tag event, and they are gated the way main is. The alternative, recording that the release line is guarded only at tag time, was rejected: it leaves a broken commit on the line until someone cuts a tag, and a tag cannot be moved once pushed. `verify` stays as it is, since it still gates the tag itself.
- **`fetch-depth: 0` in each checkout that runs `make test`.** With a shallow checkout the history that tells which tags a commit descends from is missing, whatever `fetch-tags` is set to. The checks that need the history are the ones that read tags; the `retract` ordering in `go.mod` is checked by `go mod tidy -diff`, which reads only `go.mod`.
- **The guard compares with the tags the checkout descends from, and falls back to all tags.** `git tag --merged HEAD` on `release-0.1` lists the `v0.1.x` tags, so a patch is held to its own line. Main descends from no release tag, because the tags are cut on the release branches, so the list is empty and all tags are used, as before.
- **On main the fallback to every tag is deliberate.** Main is the next minor and a release branch carries patches, so main's README is expected to lead every shipped tag. A failure there, `README.md has 0.2.3, older than the newest tag v0.2.4: update the literals`, is a true statement: the README lags a tag that shipped. The remedy is to update the README literals (`make release-prep`), not to look for a bug in the test, and the test does not skip on main, because that would hide the case it exists for.
- **In CI the guard fails, not skips, when it cannot read what it needs.** When `CI` is set and the repository is shallow, or git lists no release tag, the test fails with a message that names `fetch-depth: 0`. Outside CI it still skips, so a clone without tags can run `go test`.

## Consequences

- A cherry-pick that breaks tests or lint is caught on the release branch, by the same jobs as on main. Each push to a release branch costs one run of both workflows.
- A checkout is the whole history rather than one commit, and a workflow that runs `make test` must keep `fetch-depth: 0`; dropping it turns the README guard red, which is the intended signal.
- A release branch cut from main before its first tag has no tag it descends from, so the guard uses all tags until that first tag exists.
- A new workflow that runs the Go tests has to fetch the history; the guard's message says so.
