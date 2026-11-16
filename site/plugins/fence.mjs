// The options a code fence may carry after its language: ```go title="main.go" lineNumbers
// (docs/development/contributing-docs.md, "Code"). One parser serves check-fences, which fails the
// build with the file and line of a bad one, and the Shiki transformer, which turns the rest into the
// `pre` attributes that Prose.astro renders as the preactcn CodeBlock. A fence without options gets none.

/** The options of a fence's info string after its language. `errors` names each thing it cannot read. */
export function parseFenceMeta(meta = '') {
  const out = { title: undefined, lineNumbers: false, errors: [] }
  for (const tok of meta.match(/\w+="[^"]*"|\S+/g) ?? []) {
    const title = /^title="([^"]+)"$/.exec(tok)
    if (title) {
      if (out.title !== undefined) out.errors.push('title is given twice')
      else out.title = title[1]
    } else if (tok === 'lineNumbers') {
      if (out.lineNumbers) out.errors.push('lineNumbers is given twice')
      out.lineNumbers = true
    } else if (tok.startsWith('{')) {
      // shortcut: highlighted lines are not built, because no page points at a line yet; drop this when a page does, with CodeBlockLine variant "highlighted".
      out.errors.push(`highlighted lines (${tok}) are not supported yet`)
    } else if (/^title=/.test(tok)) {
      out.errors.push(`title needs a non-empty value in double quotes, title="main.go" (got ${tok})`)
    } else {
      out.errors.push(`unknown option "${tok}" (the options are title="…" and lineNumbers)`)
    }
  }
  return out
}

/** A Shiki transformer: the fence's options become attributes of its `pre`. */
export function fenceOptions() {
  return {
    name: 'ichor-fence-options',
    pre(node) {
      // Astro passes the info string after the language as `meta.__raw`, and only check-fences lets a bad one reach here.
      const { title, lineNumbers, errors } = parseFenceMeta(this.options.meta?.__raw)
      if (errors.length) throw new Error(errors.join('; '))
      if (title !== undefined) node.properties.dataTitle = title
      if (lineNumbers) node.properties.dataLineNumbers = ''
    },
  }
}
