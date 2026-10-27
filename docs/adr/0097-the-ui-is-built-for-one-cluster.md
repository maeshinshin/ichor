# 0097. The UI is built for one cluster

Status: Accepted · Date: 2026-10-28 · Design section: CLI and UI · Extends: 0078

Decided: Ichor manages exactly one cluster, now and later, and the UI is built for that: there is no Clusters list, the Overview is the cluster, every object URL drops the namespace, and a top bar shows the cluster's name, its Talos, Kubernetes and Ichor versions and a health badge (Ready, Degraded, Upgrading)

## Context

The UI inherited a multi-cluster shape from the CRDs: a Clusters list, `/clusters/:namespace/:name` detail pages, and `/nodes/:namespace/:name` style URLs that carried the namespace of every object. The decision is that one Ichor manages one cluster and that will not change, so the list was a page with one row and every URL carried a segment that never varied.

## Decision

**Navigation (the ADR 0078 shell).** Overview `/`, Nodes `/nodes`, Operations `/operations`, Backups `/backups`, Configuration `/config` (was Patches), GitOps `/gitops`, and Users `/users` only when users are enabled (ADR 0094). No Clusters entry.

**Top bar.** Cluster name, Talos and Kubernetes versions, the Ichor version (as before) and a health badge beside the theme switch and account menu. Health is Upgrading while a rollout runs, Ready when the cluster's `Ready` condition is True, Degraded otherwise.

**Routes.** `/`, `/nodes`, `/nodes/add`, `/nodes/:name`, `/operations`, `/operations/:name`, `/backups`, `/config`, `/config/new`, `/config/:name`, `/gitops`, `/users`. Old URLs are replaced client-side (`history.replaceState`, so Back does not loop): `/clusters` and `/clusters/:ns/:name` to `/`, `/clusters/:ns/:name/add-node` to `/nodes/add`, `/machines` to `/nodes`, `/nodes/:ns/:name` to `/nodes/:name`, `/operations/:ns/:name` to `/operations/:name`, `/patches...` to `/config...`. The table is `redirectFor` in `ui/src/lib/routes.ts`.

**One data source.** `useCluster()` lists clusters once through the existing `ListClusters` RPC, shares the result across the shell and the pages, and polls every 10 seconds while anything uses it. All objects live in the Ichor namespace, which pages take from `cluster.ref.namespace`; it is no longer in a URL. Page titles are still published per namespace, so `breadcrumbsFor` takes the namespace as an argument.

**Zero or several clusters.** With none, every page but Users shows "No cluster yet. Run `ichor init`." With more than one (a state Ichor does not create), the first by name is shown under a warning "Ichor manages one cluster; showing <name>".

## Why

One cluster is the scope; a list of one and a namespace in every URL are noise. Shorter URLs are easier to read and share, and the Overview already summarises the cluster, so a separate cluster page repeated it. Keeping the warning rather than refusing to render means a stray second cluster is visible instead of locking the UI.

## Consequences

The CRDs and RPCs stay namespaced; only the UI stops exposing it. Bookmarks to the old URLs keep working through the redirects. If a second cluster were ever wanted it would be a new decision, not a flag.
