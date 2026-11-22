# Contributing to the docs

The site is built from the Markdown files in this directory, in place. Write a
`.md` file under `docs/` and it is a page, in the right sidebar section and in
the search. The files read fine on GitHub too.

![The docs pipeline. A Markdown page goes straight to make docs-site, which builds the Astro site, checks links and code fences and builds the Pagefind index into site/dist. A diagram is edited as NAME.light.drawio.svg, make docs-diagrams generates NAME.dark.drawio.svg, and both go into the build. The diagramlabels test checks the diagram labels. main publishes as /next/, release-X.Y as /vX.Y/ and tags as /vX.Y.Z/.](../images/docs-pipeline.light.drawio.svg)

*From a file in `docs/` to the published site.*

## Preview

```sh
make docs-dev      # live preview at http://localhost:4321/ichor/next/ (search needs the built site)
make docs-site     # the real build of the development docs into site/dist, with the search index
make docs-preview  # serve site/dist
make docs-site-all # every version, as published, into site/dist-all
```

Run these inside `nix develop`, or with the tools from [The
tools](build-from-source.md#the-tools) on your `PATH`. Write commands in a page
as if the tools are on `PATH`; never put `nix develop` in a command block.
`make docs-site` fails on a broken internal link (missing file, page or
`#heading`).

## Versions

| Where | Published as |
|---|---|
| `main` | `/next/`, "Unreleased (main)" |
| branch `release-X.Y`, or the newest tag `vX.Y.*` while the branch does not exist | `/vX.Y/` |
| each tag | a frozen `/vX.Y.Z/` (a tag whose docs do not build redirects to `/vX.Y/`) |
| newest line | `/latest/` and the site root |

A docs fix for a released line goes to `release-X.Y` (cherry-pick from main, or
open the PR against that branch); the site republishes on the push, so no patch
tag is needed. The version menu comes from `versions.json`, which the workflow
writes.

Write a release to install as `{{version}}` (no leading `v`); the build
replaces it with the patch release the copy documents (for `/next/`, the newest
tag). Never write a release number in prose. `README.md` is not built, so it has
literals; `make release-prep VERSION=vX.Y.Z` sets them before the tag is pushed.

## Adding a page

Create `docs/<section>/<name>.md` starting with a `# Title` heading. Nothing
else is needed.

| Thing | Rule |
|---|---|
| URL | the path without `.md`; `README.md` or `index.md` is the directory's own page; `docs/index.md` is the home page |
| Section | the directory: `guides/`, `concepts/`, `design/`, `reference/`; any other directory is a section listed after those. `adr/` holds decision records and is not published |
| Sidebar | each page or directory directly under a section is a row; a directory, or a page with pages under it, is one row that folds its pages |
| Long page | a short page plus a directory of the same name (`design/architecture.md` and `design/architecture/`). The index keeps the old path and is listed first; children carry `sidebar_order` 1, 2, 3 ... because the previous and next links follow the sidebar |
| Title | front matter `title`, else the first `# ` heading (printed once) |
| On this page | the `##` and `###` headings |

### Front matter (optional)

```yaml title="docs/guides/rotate-keys.md" lineNumbers
---
title: A title that differs from the first heading
sidebar_label: A shorter name for the sidebar
sidebar_order: 1
---
```

Without `sidebar_order`, pages are listed alphabetically by title; lower
numbers come first and unnumbered pages after. Pages sharing a `sidebar_group`
fold behind one row, in `sidebar_order`, where the first page would sit. GitHub
shows front matter as a table, so use it sparingly.

## Links and images

Link with relative paths: `[Adopt a cluster](adopt-a-cluster.md)`,
`[the design](../design/architecture/overview.md#data-flow)`. A relative link
that leaves `docs/` (`../../internal/foo.go`) becomes a link to that file on
GitHub. A link to something that does not exist fails the build.

Images live in `docs/images/` and are referenced the same way,
`![What it shows](../images/name.svg)`. A missing image fails the build.

## Callouts

GitHub's alert syntax; the site renders it as an alert.

```markdown
> [!NOTE]
> Something worth knowing.

> [!WARNING]
> Something that can hurt.
```

Kinds: `NOTE`, `TIP`, `IMPORTANT`, `WARNING`, `CAUTION`.

## Code

Fenced blocks with a language; the site highlights them and adds a copy button.
Every block must name its language, and `make docs-site` fails with the file and
line of one that does not.

| Language | For |
|---|---|
| `sh` | shell commands |
| `console` | a session with `$ ` prompts and output |
| `yaml`, `json`, `go` | source |
| `text` | plain output, diagrams, file trees |

Options follow the language, in either order. `title` takes a double-quoted
value, `lineNumbers` numbers the lines, and `{3}` or `{3,5-7}` highlights lines
(counted from 1, no spaces; a number past the last line fails the build). Any
other word is an error. GitHub shows the block without the options. The pages
under `reference/` are labelled by `make docs-reference`.

````markdown
```yaml {3,5-7} title="main.go" lineNumbers
```
````

## Tabs

Alternatives for the same job, such as the UI way and the CLI way, go in tabs. The
block starts with `:::tabs`, each section starts with a `::tab[Label]` line followed by
a blank line, and `:::` closes the block. Text before the first section is an error.

````markdown
:::tabs

::tab[In the UI]

Open the cluster's page and choose "Reset and add".

::tab[With the CLI]

```sh
ichor reclaim node 10.0.0.21 --cluster prod
```

:::
````

GitHub shows the markers as plain lines, with the sections as ordinary Markdown
between them.

## What every page gets

Nothing here is written by hand: a "Copy page" menu, a link icon on every `##`
and `###` heading, the Markdown of every page served as `<page>.md`, and
`/llms.txt` (an index) and `/llms-full.txt` (all pages). Copied Markdown has its
relative links rewritten to full URLs.

## Diagrams

Every page carries at least one [draw.io](https://www.drawio.com/) diagram that
shows its main idea (a flow, the parts and how they connect, or a sequence);
a decorative one does not count. Diagrams live in `docs/images/` as a pair:

| File | Is |
|---|---|
| `NAME.light.drawio.svg` | the one you edit: an ordinary SVG that also carries the whole draw.io model, so draw.io (web, desktop, VS Code extension) opens it |
| `NAME.dark.drawio.svg` | the dark theme, exported from the light file, never edited by hand |

A page names the light file once, with an alt text that says what the diagram
shows and a one-line caption under it:

```markdown
![What the diagram shows, in a sentence or two.](../images/NAME.light.drawio.svg)

*A one-line caption.*
```

GitHub shows the light image; the site shows the dark one in a dark theme, and
each image links to its SVG at full size.

To change a diagram, edit and save the light file under the same name, then
export the pair:

```sh
make docs-diagrams                                              # every diagram
make docs-diagrams SOURCE=docs/images/NAME.light.drawio.svg     # one
```

It runs the draw.io command line from the dev shell (under `xvfb-run`) once per
theme. A new diagram is saved as `docs/images/NAME.drawio`;
`make docs-diagrams SOURCE=docs/images/NAME.drawio` writes the pair.

**Look.** One model gives both files because every colour is a `light-dark()`
pair in the shape's style (Edit Style, Ctrl-E), for example
`fillColor=light-dark(#EEF2FF,#232553);strokeColor=light-dark(#6366F1,#818CF8)`.
Copy a shape from an existing diagram. Neutral grey for boxes and groups,
indigo for Ichor's own parts and for what a person does, amber for what waits
or is not authenticated, green for done, red for no way back. Boxes are
rounded, connectors orthogonal with labels on the line, text 12 px or more, the
diagram about 900 px wide. The export drops the PNG copies of the labels that
draw.io adds (700 KB a diagram).

**Labels are plain SVG text, not HTML** (ADR 0132: phones render them). The
recorded cause is iOS Safari's text autosizing and WebKit's handling of
`foreignObject` inside an `<img>`; no device test is on record. The draw.io
command line cannot write labels as SVG text, so the model keeps the rule:

- The root cells and every label cell carry `html=0`; no cell has `html=1`.
- A line break is a real newline (`&#xa;` in the XML), never `<br>` or `<div>`.
- A label holds one kind of text: a bold title and the text after it are two
  cells, because `fontStyle=1` bolds a whole cell.
- Do not set `whiteSpace=wrap`; draw.io then writes a `foreignObject`. Break a
  plain label with newlines.

A cell copied from another diagram can bring `html=1` along: set `html=0` in
Edit Style, save the light file and run `make docs-diagrams` again.

`go test ./internal/tools/diagramlabels/` (also run by `make test`) fails on any
cell with `html=1`, any `<br>` or `<div>` in a label and any `foreignObject` in
an SVG under `docs/images`, and names the file and cell. A cell with no `html`
key passes, because draw.io reads it as plain text.
