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
import { spawn } from 'node:child_process'
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
// Each export leads its own process group: xvfb-run, the Xvfb it starts and draw.io's Electron processes stay in it.
// A signal to xvfb-run alone leaves Xvfb and Electron running (reparented to init), so a timeout kills the group.
let running = 0 // the group of the export in progress; Ctrl-C and SIGTERM reach this process only, not the group
const killGroup = (pgid) => {
  try {
    process.kill(-pgid, 'SIGKILL')
  } catch (e) {
    if (e.code !== 'ESRCH') throw e // ESRCH: the group has already ended
  }
}
process.on('exit', () => {
  if (running) killGroup(running)
})
process.on('SIGINT', () => process.exit(130))
process.on('SIGTERM', () => process.exit(143))

const runExport = (cmd, args) =>
  new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { stdio: 'ignore', detached: true }) // stdio ignored: Electron's GPU warnings are noise
    running = child.pid
    const finish = (err) => {
      clearTimeout(timer)
      running = 0
      if (err) reject(err)
      else resolve()
    }
    const timer = setTimeout(() => {
      killGroup(child.pid)
      finish(new Error('no answer in 120 s'))
    }, 120000)
    child.once('error', finish)
    child.once('exit', (code) => finish(code === 0 ? null : new Error(`exit ${code}`)))
  })

const exportSvg = async (src, theme, out) => {
  const args = ['-x', '-f', 'svg', '-e', '-u', '--theme', theme, '-o', out, src] // -e embeds the model, -u keeps it readable (diffs)
  const [cmd, ...pre] = process.env.DISPLAY ? ['drawio'] : ['xvfb-run', '-a', 'drawio']
  for (let attempt = 1; ; attempt++) {
    try {
      return await runExport(cmd, [...pre, ...args])
    } catch (e) {
      if (attempt === attempts) throw new Error(`draw.io did not export ${src} (${theme}) in ${attempts} tries: ${e.message}`)
      await new Promise((resolve) => setTimeout(resolve, 500 + Math.random() * 2500))
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
      await exportSvg(src, theme, raw)
      const out = join(dir, `${name}.${theme}.drawio.svg`)
      writeFileSync(out, lean(readFileSync(raw, 'utf8')))
      console.log(out)
    }
  }
} finally {
  rmSync(tmp, { recursive: true, force: true })
}
