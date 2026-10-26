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
