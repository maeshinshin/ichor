# 0146. A release-branch push republishes the docs through a run on main

Status: Accepted · Date: 2026-11-11 · Design section: Documentation

Decided: GitHub Pages takes the docs deployment from `main` only, so the docs workflow builds and deploys only on `main`, on a dispatch, and after a successful Release. A push to `release-*` runs one job, `republish`, that runs `gh workflow run docs.yml --ref main`, and that job alone has `actions: write`. The dispatched run is a `workflow_dispatch` event, which the job's condition excludes, so nothing chains. The `github-pages` environment keeps its policy: `main` only.

## Context

ADR 0142 made a push to `release-*` trigger the docs workflow. The build succeeds on that branch and `actions/deploy-pages` fails there, because the `github-pages` environment accepts deployments from `main` only. Two runs close together (one from `main`, one from a release branch) also compete for one deployment slot.

## Decisions

1. **The deploy stays on main.** Only `main`'s run reaches the Pages deployment, so the deployment branch policy does not change.
2. **A release-branch push asks main for a run.** The build reads every `release-*` branch when it runs, so a run on `main` publishes the push. The job needs no checkout and no token beyond `GITHUB_TOKEN`.
3. **Loop guard by event, not by flag.** The dispatched run is `workflow_dispatch`, and the job only runs for `push`. A `GITHUB_TOKEN` push cannot start a workflow, and a dispatch cannot start one of this workflow's `push` jobs.
4. **Concurrency unchanged.** The `pages` group keeps `cancel-in-progress: false`. A deployment that is uploading is never cut off, and a queued run is replaced by a newer one, which builds the newest state of every branch when it starts.

## Rejected

- Widening the Pages deployment policy to `release-*`: a workflow on any release branch could then publish to the live site.
- Building on the release branch and handing the artifact to a `main` run: the artifact would have to cross runs, which this workflow does not do.
- `repository_dispatch`: it needs the same token permission and a second trigger for the same job, for no gain over a dispatch of this file.

## Consequence

A release-branch push is live a little later than before: the republish job waits in the `pages` group if a deployment is running (the group is set on the whole workflow, so the job cannot be exempted), then the run on `main` builds and deploys. A push to `main` that touches the docs still publishes once, from its own run.
