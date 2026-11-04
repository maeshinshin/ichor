# 0078. UI app shell: sidebar, breadcrumb bar, and "On this page" section navigation

Status: Accepted · Date: 2026-10-26 · Design section: UI
Decided: The UI is a left sidebar (preactcn `sidebar`, icons-collapsible, a sheet on phones) plus a sticky top bar with a `breadcrumb`; detail pages have a header and card sections with stable ids, and long ones get a sticky right-hand "On this page" list instead of tabs
Supersedes: 0074 (the header and nav-row part only; the scrollbar-gutter and scroll-lock decisions stay)

## Context

The top header with five nav buttons worked on a phone only because the nav row scrolled sideways (ADR 0074), and detail pages were a vertical stack of identically styled cards with no overview. The user asked for something closer to Argo CD, the AWS console or Rancher: persistent navigation, a breadcrumb, readable summaries, and a way around the long machine page.

## Decision

1. **Shell** (`components/AppShell.tsx`). preactcn `sidebar` with `collapsible="icon"`: product name on top, Clusters / Machines / Patches / Operations / GitOps with lucide icons (already the project's icon set), and in the footer, when sign-in is not configured, a compact warning whose full text is a tooltip. Below 768px the component renders the same content as a `sheet`, opened from the top bar's `SidebarTrigger`; choosing an entry closes it. The sidebar tokens were added to `index.css` (light and dark). A nav entry is active on its list page and on the detail pages under it.
2. **Top bar.** Sticky, with the trigger and a `breadcrumb` from the pure `breadcrumbsFor(path)` (`lib/breadcrumbs.ts`), e.g. Clusters / ichor-ui-demo. The current crumb truncates; the list does not wrap. The light/dark switch (ADR 0070) sits at the bar's right end as a ghost icon button, matching the preactcn site's theme toggle.
3. **Phone guarantees from ADR 0074 are kept**: the inset column is `min-w-0`, tables scroll inside their own containers, `html { scrollbar-gutter: stable }` and the dialog scroll lock are untouched. The old wrapping header is gone because nothing in the bar can widen the page.
4. **Pages.** `PageHeader` (name, kind, status badges, primary actions), `Fields` / `Field` (description-list grid), `Section` (a card with a stable id), `PageSkeleton` while loading (the loading text stays as an `sr-only` status), and `EmptyNote` (preactcn `empty`) for empty lists and sections. Handover and other lists became tables.
5. **Section navigation instead of tabs.** `SectionLayout` shows a sticky "On this page" list in a right-hand column from `xl` up; below that it becomes a sticky row of jump links that scrolls inside itself. The current section is computed by the pure `activeSection(tops, offset)` (`lib/sectionNav.ts`) from the sections' measured tops on scroll and resize, once per animation frame, rather than with an `IntersectionObserver`: with sections of very different heights, "last section whose top passed the header" is what the reader expects, and an observer's threshold cannot express that without extra bookkeeping. At the very bottom the last section is active even if it is too short to reach the offset.
   Tabs were rejected for the machine page: Talos details, the effective config and the log stream each load on mount, and tabs would either mount them all anyway or restart the log stream on every tab switch; one scrolling page also keeps every section linkable by `#id` and printable. The machine page has ten sections, which is what the list is for; the cluster and patch pages use the same component with five and three.
6. **Machine page** starts with an overview (hostname, role, cluster, approval, Ready, stage, endpoint, Talos version of the cluster, kubelet version), loaded with `GetMachine` and `GetCluster`; if either fails the page still renders from `GetNode`. `TalosDetails` is unchanged apart from sitting in a section anchor.

## Consequences

- preactcn components added: `sidebar`, `sheet`, `tooltip`, `breadcrumb`, `skeleton`, `empty` (and their radix ports `tooltip`; `tabs`, `scroll-area` and `collapsible` were installed, found unused and removed). The installer was told not to overwrite existing shared files, so the ADR 0074 `modal-layer.ts` is kept.
- The sidebar keeps its open/closed state in a cookie, as the component does; the `Ctrl/Cmd+B` shortcut comes with it.
- Not verified in a real browser (none in `nix develop`); checked by building and reading the markup and classes.

## Amendment: first look in a browser (2026-10-26)

The shell was first viewed in headless Chromium (`make ui-shots`, `hack/ui-shots.mjs`; Chromium is in `flake.nix`) against the demo. Measured against Argo CD and the cloud consoles, it was a stack of cards with grey pills for status and no landing page. Changes:

1. **Overview is the landing page** (`/`, `pages/Overview.tsx`): a strip of four linked count tiles (clusters, machines, operations, patches) with health lines, a "Needs attention" table (pending approvals, not-ready machines, running/paused/failed operations, rollouts) and the latest operations. The cluster list moved to `/clusters`; breadcrumbs and the sidebar follow. Each list is read separately, so a list the user may not read shows "not available" instead of failing the page. Counting is in `lib/overview.ts`.
2. **List pages** share `ListToolbar` (preactcn `input-group` search, count, refresh, the page's primary action), `DataTable` (one bordered table, no card around it) and `LinkRow` (the whole row opens the detail page; links and buttons in it keep their own action). Search is `lib/filter.ts`. Empty "pending" and "rejected" machine sections are hidden rather than shown as empty cards.
3. **Status is a coloured icon plus the word** (`components/Status.tsx`, tone from `lib/status.ts`), not a pill; colour is never the only cue. Machine readiness shows the condition's reason (Converged) when there is one.
4. **Detail pages**: `PageHeader` takes a `meta` list of compact label/value pairs under the title, so the key facts show without scrolling; the "Overview" sections of the cluster and machine pages are gone (their fields moved there). `Section` is a heading with a rule, not a card; tables inside a section get the same bordered frame. Conditions are a table.
5. The theme switch was compared with the preactcn site's `ThemeToggle.tsx`: same markup (ghost icon `Button`, `Sun hidden dark:block`, `Moon dark:hidden`); only the storage key differs (`ichor-ui-theme`, ADR 0070).
6. `ICHOR_API=http://host npx vite` (and `vite preview`) proxy the Connect services and `/auth/config` to a running backend for screenshots.
7. **The UI says "Node", not "Machine"**: the sidebar entry, page titles, breadcrumbs, Overview tile and table headers, dialogs and kind labels read "Node" (`lib/i18n.ts`). The `/machines` route, the i18n key names, the protos and the `TalosMachine` CRD keep their names.

## Amendment: the cluster page's Operations section shows context (2026-10-27)

The section was a row of seven buttons with no context. It is now `components/OperationsPanel.tsx`:

1. **Current operation** on top, when one of the cluster is not finished: kind, state, "n of m nodes done", the node in progress, the failure text, a link to the operation page, and pause or resume (`OperationControls` with `showDelete={false}`). It is read every 5 seconds. "Not finished" is the server's own rule (`unfinishedOperation`: not Completed, except a node that was shut down), kept in `lib/clusterOps.ts`.
2. **Three groups**, each action a row with a short description, the state now and one button. *Versions*: Talos, Kubernetes and Ichor with the installed version ("Upgrade…"). *Configuration*: apply pending config (how many nodes wait for a reboot, and which), kube-proxy mode ("Switch…"), certificate authorities with their expiry dates, marked when under 30 days or past ("Rotate…"). *Danger zone*: shut down the cluster.
3. **A button is off, with the reason written next to it**, in two cases: another operation is not finished, and "apply pending config" when no node waits. The server still refuses the same cases; this only saves the trip.
4. **The upgrade dialog** starts with the installed version in the field, shows "Now vX. Enter a newer version." and keeps "Review" off until the target is a semantic version newer than the installed one (`versionProblem`; the server refuses a downgrade too). The kube-proxy dialog starts on the other mode. Review (the plan) and Start are unchanged.
5. **The last five operations** of the cluster, newest first, with a state icon and a link.
6. **Data**: the Cluster message gained `kube_proxy_mode` and `certificates` (status.certificates, ADR 0063), the Machine message `reboot_paths` (status.rebootPaths, ADR 0080). The installed Ichor version comes from `PlanOperation` (kind ichor, no target), so no new RPC. A node counts as waiting when `reboot_paths` is set or its Ready reason is RebootRequired. "Update available" for Ichor is not shown: nothing in the cluster knows the latest release.
7. Phone: groups are cards, the button drops under the text; no horizontal scroll.

## Amendment (2026-10-28): no section navigation

The "On this page" scroll-spy (`SectionLayout`, `lib/sectionNav.ts`) is gone. Users found it highlighted the wrong entry, the node page now uses tabs, and the Overview is the cluster home (ADR 0097), so only the patch page still had it. Pages stack their `Section` blocks (`components/Section.tsx`); section ids stay, so `#anchor` links still work.

## Amendment (2026-11-04): the Overview no longer switches its buttons off while an operation runs

Item 3 changed for the "another operation is not finished" case. The buttons stay on and the card no longer says "Waiting for the current operation to finish." Starting anything on a cluster with an unfinished operation is still refused by the server (`unfinishedOperation`, used by `CreateOperation` and `StartCniUpgrade`), because every TalosOperation takes the cluster lock under one identity, so the lock would not keep two of them apart. The dialog now shows that refusal, which names the operation and its phase: the plan's "Blocked" alert for the operation dialogs, the error alert for the Cilium upgrade.
