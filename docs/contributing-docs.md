# Contributing to the docs

The documentation site is built from the Markdown files in this directory, in
place. There is nothing to register: write or edit a `.md` file under `docs/`,
and it is a page of the site, in the right section of the sidebar, found by the
search. The same files read fine on GitHub.

## Preview

```sh
make docs-dev      # live preview at http://localhost:4321/ichor/next/ (search needs the built site)
make docs-site     # the real build of the development docs into site/dist, with the search index
make docs-preview  # serve site/dist
make docs-site-all # every version, as published, into site/dist-all
```

Everything runs inside `nix develop`. `make docs-site` fails on a broken
internal link (a missing file, a missing page or a missing `#heading`), so a
Markdown-only change cannot publish one.

## Versions

You only ever edit the Markdown on `main`. That is published as the
development docs (`/next/`, "Unreleased (main)"). Each release tag `vX.Y.Z`
publishes the `docs/` it was tagged with as `/vX.Y.Z/`, and `/latest/` and the
site root lead to the newest release, so a release's docs are frozen by its tag
and nothing is copied. The version menu in the header lists them from
`versions.json`, which the workflow writes from the tags.

## Adding a page

Create `docs/<section>/<name>.md`, starting with a `# Title` heading. That is
all that is needed:

- **The URL** is the path without `.md`: `docs/guides/rotate-keys.md` is
  `/guides/rotate-keys/`. `README.md` or `index.md` in a directory is that
  directory's own page, and `docs/index.md`
  is the home page.
- **The section** is the directory. `guides/` is Guides, `design/` is Concepts,
  `reference/` is Reference and `adr/` is Decisions (newest first, collapsed
  until you are in it). Any other directory becomes a section of its own, listed
  after those; a directory inside a section becomes a collapsible group.
- **The title** is the front matter `title`, else the first `# ` heading. The
  site prints it as the page heading, so the Markdown's own `#` line is not
  shown twice.
- **The headings** `##` and `###` make the "On this page" list.

## Front matter (optional)

```yaml
---
title: A title that differs from the first heading
sidebar_label: A shorter name for the sidebar
sidebar_order: 1
---
```

Without `sidebar_order`, pages in a section are listed alphabetically by
title. Give a page a number to put it first (lower numbers come first; pages
without one come after). GitHub shows front matter as a small table, so use it
sparingly.

## Links and images

Link to other pages the way you would on GitHub, with relative paths:
`[Adopt and reclaim](adopt-and-reclaim.md)`, `[the design](../design/architecture.md#trust)`.
They become site links. A relative link that leaves `docs/`
(`../../internal/foo.go`, `../../test/harness/`) becomes a link to that file or
directory on GitHub. A link to something that does not exist fails the build.

## Callouts

Use GitHub's alert syntax; GitHub renders it too, and the site shows it as an
alert.

```markdown
> [!NOTE]
> Something worth knowing.

> [!WARNING]
> Something that can hurt.
```

The kinds are `NOTE`, `TIP`, `IMPORTANT`, `WARNING` and `CAUTION`.

> [!TIP]
> This box is the `TIP` kind.

## Code

Use fenced blocks with a language (` ```sh `, ` ```yaml `, ` ```go `). The site
highlights them (light and dark) and adds a copy button; there is nothing to add
by hand.

Every block must name its language, and `make docs-site` fails with the file
and line of one that does not. Use `sh` for shell commands, `console` for a
session that shows `$ ` prompts with their output, `yaml`, `json` and `go` for
source, and `text` for plain output, diagrams and file trees. The generated
pages under `reference/` are labeled by `make docs-reference`.

## What every page gets

Nothing here is written by hand. Each page has a "Copy page" menu (copy the
Markdown, view it, copy the link, share on devices that can), every `##` and
`###` heading has a link icon that copies the address of that section, and the
Markdown of every page is served as `<page>.md` next to it, with `/llms.txt`
(an index) and `/llms-full.txt` (all pages) for tools that read the docs. The
copied Markdown has its relative links rewritten to full URLs, so it still
works outside the site.
