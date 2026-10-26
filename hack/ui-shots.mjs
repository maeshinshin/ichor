// Screenshots of the UI at desktop and phone widths, light and dark, with
// headless Chromium (from the dev shell) over the DevTools protocol. No npm
// dependency: Node's own WebSocket and fetch are enough.
//
//   node hack/ui-shots.mjs <outDir> <baseURL> [path ...]
//
// CLICK="Button text" clicks that button first (to show a dialog); never use it on a button that writes.
// SCROLLTO=id scrolls that element to the top first (use with FULL unset).
// FULL=1 captures the whole page height (up to 4000px), not just the viewport.
// Paths default to the list pages. Read-only: it only navigates (GET).
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

const [outDir, base, ...rest] = process.argv.slice(2)
if (!outDir || !base) {
  console.error('usage: node hack/ui-shots.mjs <outDir> <baseURL> [path ...]')
  process.exit(2)
}
const paths = rest.length ? rest : ['/', '/clusters', '/machines', '/patches', '/operations', '/gitops']
const sizes = [
  ['desktop', 1440, 900, false],
  ['phone', 390, 844, true],
]
const themes = ['light', 'dark']
const port = 9300 + Math.floor(Math.random() * 500)
mkdirSync(outDir, { recursive: true })

const chrome = spawn(
  process.env.CHROMIUM ?? 'chromium',
  ['--headless=new', '--no-sandbox', '--disable-gpu', `--remote-debugging-port=${port}`, '--user-data-dir=' + join(outDir, '.profile'), 'about:blank'],
  { stdio: 'ignore' },
)
try {
  let version
  for (let i = 0; i < 100 && !version; i++) {
    try {
      version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
    } catch {
      await sleep(100)
    }
  }
  const ws = new WebSocket(version.webSocketDebuggerUrl)
  await new Promise((r) => (ws.onopen = r))
  let id = 0
  const pending = new Map()
  ws.onmessage = (m) => {
    const d = JSON.parse(m.data)
    if (d.id && pending.has(d.id)) pending.get(d.id)(d)
  }
  const send = (method, params = {}, sessionId) =>
    new Promise((resolve, reject) => {
      const n = ++id
      pending.set(n, (d) => (d.error ? reject(new Error(method + ': ' + d.error.message)) : resolve(d.result)))
      ws.send(JSON.stringify({ id: n, method, params, sessionId }))
    })
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
  const s = (m, p) => send(m, p, sessionId)
  await s('Page.enable')

  for (const p of paths) {
    for (const [sizeName, width, height, mobile] of sizes) {
      for (const theme of themes) {
        await s('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile })
        await s('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] })
        const { identifier } = await s('Page.addScriptToEvaluateOnNewDocument', {
          source: `try{localStorage.setItem('ichor-ui-theme','${theme}')}catch(e){}`,
        })
        await s('Page.navigate', { url: new URL(p, base).href })
        await sleep(Number(process.env.WAIT ?? 3500)) // data fetches and the skeleton settle (WAIT=ms for slow pages)
        if (process.env.CLICK) {
          // CLICK="Shut down cluster" opens a dialog before the shot; it clicks the first button with that exact text.
          const expr = `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${JSON.stringify(process.env.CLICK)}); if (b) b.click(); return !!b })()`
          await s('Runtime.evaluate', { expression: expr })
          await sleep(800)
        }
        if (process.env.SCROLLTO) {
          await s('Runtime.evaluate', { expression: `document.getElementById(${JSON.stringify(process.env.SCROLLTO)})?.scrollIntoView()` })
          await sleep(1500)
        }
        const shot = { format: 'png' }
        if (process.env.FULL) {
          const { cssContentSize } = await s('Page.getLayoutMetrics')
          shot.captureBeyondViewport = true
          shot.clip = { x: 0, y: 0, width, height: Math.min(Math.ceil(cssContentSize.height), 4000), scale: 1 }
        }
        const { data } = await s('Page.captureScreenshot', shot)
        const slug = (p === '/' ? 'home' : p.replace(/^\/+/, '').replace(/[^a-zA-Z0-9]+/g, '-')) || 'home'
        const file = join(outDir, `${slug}.${sizeName}.${theme}.png`)
        writeFileSync(file, Buffer.from(data, 'base64'))
        console.log(file)
        await s('Page.removeScriptToEvaluateOnNewDocument', { identifier })
      }
    }
  }
  ws.close()
} finally {
  chrome.kill()
}
