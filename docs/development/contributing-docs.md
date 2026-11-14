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

This guide's commands are the one place that assumes the dev shell: run them
inside `nix develop`, or with the tools from
[The tools](build-from-source.md#the-tools) on your `PATH`.
When you write commands in a page, write them as if the tools are on `PATH`;
do not put `nix develop` or `nix develop -c` in a command block. `make docs-site` fails on a broken
internal link (a missing file, a missing page or a missing `#heading`), so a
Markdown-only change cannot publish one.

## Versions

`main` is published as the development docs (`/next/`, "Unreleased (main)").
Each minor release line has one copy, `/vX.Y/`, built from the branch
`release-X.Y`, or from the newest tag `vX.Y.*` while that branch does not exist.
A docs fix for a released line goes to `release-X.Y` (cherry-pick it from main,
or open the PR against that branch); the site republishes on the push, so no
patch tag is needed for docs. Each tag also has a frozen copy, `/vX.Y.Z/`, built
from that tag (a tag whose docs do not build redirects to `/vX.Y/`), and `/latest/` and the site root lead to the
newest line. The version menu in the header lists the lines and, under the minor being read, its patches, from `versions.json`, which the workflow writes.

A page that tells the reader which release to install writes it as `{{version}}`
(no leading `v`), which the build replaces with the patch release that copy
documents (for `/next/`, the newest tag). Do not write a release number in prose.
`README.md` is not built, so it has literals; `make release-prep VERSION=vX.Y.Z` sets them
before the release tag is pushed.

## Adding a page

Create `docs/<section>/<name>.md`, starting with a `# Title` heading. That is
all that is needed:

- **The URL** is the path without `.md`: `docs/guides/rotate-keys.md` is
  `/guides/rotate-keys/`. `README.md` or `index.md` in a directory is that
  directory's own page, and `docs/index.md`
  is the home page.
- **The section** is the directory. `guides/` is Guides, `concepts/` is Concepts,
  `design/` is Design, `reference/` is Reference. Any other directory becomes a section of its own, listed
  after those. Every section is a flat list of its pages: a directory inside a section
  adds its pages to that list in place, with no group to open.
  `adr/` holds the decision records, kept in the repository and not published on the site.
- **A long page** is split into a short page and a directory of the same name:
  `design/architecture.md` is the index and `design/architecture/` holds one page
  per section. The index keeps the old path, so links to it still work, and is
  listed first in its group. Each child's title is the heading it had, and the
  children carry `sidebar_order` 1, 2, 3, ... because the previous and next
  links follow the sidebar and title order is rarely reading order.
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
`[Adopt a cluster](adopt-and-reclaim.md)`, `[the design](../design/architecture/overview.md#data-flow)`.
They become site links. A relative link that leaves `docs/`
(`../../internal/foo.go`, `../../test/harness/`) becomes a link to that file or
directory on GitHub. A link to something that does not exist fails the build.

Images live in `docs/images/` and are referenced the same way,
`![What it shows](../images/name.svg)`; the site serves the SVG and PNG files of
`docs/` at their own path. A missing image fails the build too. Diagrams have
their own section below.

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

## Diagrams

Diagrams are [draw.io](https://www.drawio.com/) files in `docs/images/`, always
a pair:

- `NAME.light.drawio.svg` is the one you edit. It is an ordinary SVG, so GitHub
  and the site show it as an image, and it carries the whole draw.io model, so
  draw.io (the web app, the desktop app or the VS Code Draw.io extension) opens
  it as a diagram.
- `NAME.dark.drawio.svg` is the same diagram in the dark theme. It is exported
  from the light file and never edited by hand.

A page names the light file once, with an alt text that says what the diagram
shows and a one-line caption under it:

```markdown
![What the diagram shows, in a sentence or two.](../images/NAME.light.drawio.svg)

*A one-line caption.*
```

GitHub shows the light image. The site shows the dark one when the reader's
theme is dark, and each image links to its SVG, which opens at full size (the
page column is narrower than the diagram).

To change a diagram, open `docs/images/NAME.light.drawio.svg` in draw.io, edit
it and save it under the same name, then export the pair again:

```sh
make docs-diagrams                                              # every diagram
make docs-diagrams SOURCE=docs/images/NAME.light.drawio.svg     # one
```

`make docs-diagrams` runs the draw.io command line from the dev shell (under
`xvfb-run`, as draw.io is an Electron app), once for each theme. A new diagram
is drawn the same way and saved as `docs/images/NAME.drawio`;
`make docs-diagrams SOURCE=docs/images/NAME.drawio` writes the pair.

One model gives both files because every colour in it is a `light-dark()` pair,
which draw.io writes into the shape's style (Edit Style, Ctrl-E in draw.io), for
example `fillColor=light-dark(#EEF2FF,#232553);strokeColor=light-dark(#6366F1,#818CF8)`.
Copy a shape from an existing diagram to keep the look, which is one palette
throughout: neutral grey for boxes and groups, indigo for Ichor's own parts and
for what a person does, amber for what waits or is not authenticated, green for
done and red for no way back. Boxes are rounded, connectors are orthogonal with
labels on the line, text is 12 px or more, and a diagram is about 900 px wide.
The export drops the PNG copies of the labels that draw.io adds for viewers
without `foreignObject` (700 KB a diagram), as browsers and GitHub draw the live
text.
