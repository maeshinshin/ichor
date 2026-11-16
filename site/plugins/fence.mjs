// The options a code fence may carry after its language: ```go title="main.go" lineNumbers {3,5-7}
// (docs/development/contributing-docs.md, "Code"). One parser serves check-fences, which fails the
// build with the file and line of a bad one, and the Shiki transformer, which turns the rest into the
// `pre` attributes that Prose.astro renders as the preactcn CodeBlock. A fence without options gets none.

/** The options of a fence's info string after its language. `errors` names each thing it cannot read. */
export function parseFenceMeta(meta = '') {
  const out = { title: undefined, lineNumbers: false, highlight: [], errors: [] }
  for (const tok of meta.match(/\w+="[^"]*"|\S+/g) ?? []) {
    const title = /^title="([^"]+)"$/.exec(tok)
    if (title) {
      if (out.title !== undefined) out.errors.push('title is given twice')
      else out.title = title[1]
    } else if (tok === 'lineNumbers') {
      if (out.lineNumbers) out.errors.push('lineNumbers is given twice')
      out.lineNumbers = true
    } else if (tok.startsWith('{')) {
      const list = /^\{(\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*)\}$/.exec(tok)
      if (!list) out.errors.push(`highlighted lines (${tok}) are line numbers and ranges, like {3,5-7}`)
      else if (out.highlight.length) out.errors.push('highlighted lines are given twice')
      else {
        // Each range is inclusive; a range that ends before it starts, or line 0, is a typo.
        for (const part of list[1].split(',')) {
          const [a, b = a] = part.split('-').map(Number)
          if (a < 1 || b < a) out.errors.push(`highlighted line range ${part} is empty or starts at 0`)
          else for (let n = a; n <= b; n++) out.highlight.push(n)
        }
      }
    } else if (/^title=/.test(tok)) {
      out.errors.push(`title needs a non-empty value in double quotes, title="main.go" (got ${tok})`)
    } else {
      out.errors.push(`unknown option "${tok}" (the options are title="…", lineNumbers and {lines})`)
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
      const { title, lineNumbers, highlight, errors } = parseFenceMeta(this.options.meta?.__raw)
      if (errors.length) throw new Error(errors.join('; '))
      if (title !== undefined) node.properties.dataTitle = title
      if (lineNumbers) node.properties.dataLineNumbers = ''
      if (highlight.length) node.properties.dataHighlight = highlight.join(' ')
    },
  }
}
