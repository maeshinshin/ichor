// Exports the docs diagrams (docs/images/NAME.light.drawio.svg and NAME.dark.drawio.svg) with the draw.io
// command line. The light file is the one you edit: it is an SVG that carries the whole draw.io model, so
// draw.io (web, desktop, or the VS Code extension) opens it as a diagram. This script exports it again
// in both themes (the colours in the model are light-dark() pairs), so the two files never drift apart.
//
//   node hack/docs-diagrams.mjs [SOURCE ...]     (make docs-diagrams)
//
// A SOURCE is a NAME.light.drawio.svg (default: every one in docs/images) or a plain NAME.drawio, which
// writes the pair next to docs/images. Run it inside `nix develop`: draw.io is Electron and wants a display,
// so xvfb-run provides one when DISPLAY is not set.
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join } from 'node:path'

const images = 'docs/images'
const sources = process.argv.length > 2 ? process.argv.slice(2) : readdirSync(images).filter((f) => f.endsWith('.light.drawio.svg')).map((f) => join(images, f))
if (!sources.length) {
  console.error(`no ${images}/*.light.drawio.svg to export`)
  process.exit(1)
}

// `xvfb-run -a` picks a free display number and loses the race when another xvfb-run starts at the same moment (two of
// three simultaneous runs fail), and a draw.io that never answers would wait for ever. So each export is bounded (a
// normal one takes a few seconds) and tried again, after a pause that differs from run to run, when it fails or stalls.
const attempts = 5
const exportSvg = (src, theme, out) => {
  const args = ['-x', '-f', 'svg', '-e', '-u', '--theme', theme, '-o', out, src] // -e embeds the model, -u keeps it readable (diffs)
  const [cmd, ...pre] = process.env.DISPLAY ? ['drawio'] : ['xvfb-run', '-a', 'drawio']
  for (let attempt = 1; ; attempt++) {
    try {
      // Electron's GPU warnings are noise. SIGTERM lets xvfb-run stop its Xvfb.
      return execFileSync(cmd, [...pre, ...args], { stdio: ['ignore', 'ignore', 'ignore'], timeout: 120000, killSignal: 'SIGTERM' })
    } catch (e) {
      if (attempt === attempts) throw new Error(`draw.io did not export ${src} (${theme}) in ${attempts} tries: ${e.message}`)
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 500 + Math.random() * 2500)
    }
  }
}

// draw.io adds a PNG of every label for viewers without foreignObject (700 KB per diagram). Browsers, GitHub
// and the site all draw the live text, so the rasters are dropped.
const lean = (svg) => svg.replace(/(<\/foreignObject>)<image [^>]*xlink:href="data:image\/png;base64,[^"]*"\/>(<\/switch>)/g, '$1$2')

const tmp = mkdtempSync(join(tmpdir(), 'ichor-diagrams-'))
try {
  for (const src of sources) {
    const dir = src.endsWith('.drawio') ? images : dirname(src)
    const name = basename(src).replace(/(\.light)?\.drawio(\.svg)?$/, '')
    for (const theme of ['light', 'dark']) {
      const raw = join(tmp, `${name}.${theme}.svg`)
      exportSvg(src, theme, raw)
      const out = join(dir, `${name}.${theme}.drawio.svg`)
      writeFileSync(out, lean(readFileSync(raw, 'utf8')))
      console.log(out)
    }
  }
} finally {
  rmSync(tmp, { recursive: true, force: true })
}
