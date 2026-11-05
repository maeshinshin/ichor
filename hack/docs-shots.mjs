// Drives the built docs site (site/dist) in headless Chromium (from the dev shell) over the
// DevTools protocol: serves dist under the Pages base, takes screenshots at desktop and phone
// width in light and dark, and checks search, the hideable sidebar, relative links and that the
// phone layout does not scroll sideways. Exits 1 on a failed check. No npm dependency.
//
//   node hack/docs-shots.mjs <outDir> [distDir]     (make docs-check runs it after make docs-site)
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { extname, join, normalize } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

const [outDir, dist = 'site/dist'] = process.argv.slice(2)
if (!outDir) {
  console.error('usage: node hack/docs-shots.mjs <outDir> [distDir]')
  process.exit(2)
}
const base = (process.env.DOCS_BASE ?? '/ichor').replace(/\/?$/, '/')
mkdirSync(outDir, { recursive: true })

const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.svg': 'image/svg+xml', '.wasm': 'application/wasm' }
const server = createServer((req, res) => {
  const path = decodeURIComponent(new URL(req.url, 'http://x').pathname)
  if (!path.startsWith(base)) return void res.writeHead(404).end()
  let file = join(dist, normalize(path.slice(base.length)))
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html')
  if (!existsSync(file)) return void res.writeHead(404).end()
  res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream' }).end(readFileSync(file))
})
await new Promise((r) => server.listen(0, '127.0.0.1', r))
const origin = `http://127.0.0.1:${server.address().port}`

const port = 9300 + Math.floor(Math.random() * 500)
const chrome = spawn(
  process.env.CHROMIUM ?? 'chromium',
  ['--headless=new', '--no-sandbox', '--disable-gpu', `--remote-debugging-port=${port}`, '--user-data-dir=' + join(outDir, '.profile'), 'about:blank'],
  { stdio: 'ignore' },
)
let failed = 0
const check = (ok, what) => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${what}`)
  if (!ok) failed++
}

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

  const ev = async (expression) => (await s('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })).result?.value
  const until = async (expression, what, ms = 10000) => {
    for (const end = Date.now() + ms; Date.now() < end; await sleep(100)) if (await ev(expression)) return true
    check(false, `timed out waiting for ${what}`)
    return false
  }
  // Hydrated: every island has dropped its ssr attribute.
  const ready = () => until(`document.readyState === 'complete' && !document.querySelector('astro-island[ssr]')`, 'hydration')
  const shot = async (name) => {
    await sleep(400)
    const { data } = await s('Page.captureScreenshot', { format: 'png' })
    const file = join(outDir, `${name}.png`)
    writeFileSync(file, Buffer.from(data, 'base64'))
    console.log(`shot ${file}`)
  }
  const open = async (path) => {
    await s('Page.navigate', { url: origin + base + path })
    await sleep(300)
    await ready()
  }
  const click = (selector) => ev(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if (e) e.click(); return !!e })()`)
  const key = async (k, modifiers = 0) => {
    for (const type of ['keyDown', 'keyUp']) await s('Input.dispatchKeyEvent', { type, key: k, code: `Key${k.toUpperCase()}`, modifiers })
  }

  for (const [size, width, height, mobile] of [['desktop', 1280, 800, false], ['phone', 375, 812, true]]) {
    for (const theme of ['light', 'dark']) {
      const tag = `${size}-${theme}`
      await s('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile })
      await s('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] })
      await s('Page.navigate', { url: origin + base })
      await sleep(300)
      await ev('localStorage.clear()')

      await open('guides/rotate-keys/')
      check((await ev(`document.documentElement.classList.contains('dark')`)) === (theme === 'dark'), `${tag}: follows the system theme`)
      check(await ev(`document.documentElement.scrollWidth <= window.innerWidth`), `${tag}: no sideways scroll`)
      await shot(`${tag}-page`)

      // Search: the Pagefind index answers "rotate" with the key-rotation guide.
      await key('k', 2)
      check(await until(`!!document.querySelector('[data-slot=command-input]')`, 'the search dialog'), `${tag}: Ctrl-K opens search`)
      await s('Input.insertText', { text: 'rotate' })
      await until(`document.querySelectorAll('[data-slot=command-item]').length > 0`, 'search results')
      check((await ev(`document.querySelectorAll('[data-slot=command-item]').length`)) > 0, `${tag}: search for "rotate" has results`)
      await shot(`${tag}-search`)
      await key('Enter')
      check(await until(`location.pathname.startsWith(${JSON.stringify(base)}) && !location.pathname.endsWith('/guides/rotate-keys/') || location.hash !== ''`, 'navigation from a result', 5000), `${tag}: Enter opens the result`)
      await ready()

      // Relative links between Markdown files became site URLs that resolve.
      await open('guides/getting-started/')
      const links = await ev(`[...document.querySelectorAll('article a[href^="${base}"]')].map((a) => a.getAttribute('href').split('#')[0])`)
      const bad = []
      for (const l of [...new Set(links)]) if ((await fetch(origin + l)).status !== 200) bad.push(l)
      check(links.length > 0 && !bad.length, `${tag}: ${links.length} internal links in getting-started, ${bad.length} broken ${bad.join(' ')}`)

      if (!mobile) {
        // The sidebar hides, the choice is remembered, the content widens.
        const w = () => ev(`document.querySelector('article').getBoundingClientRect().width`)
        const before = await w()
        await click('[data-slot=sidebar-trigger]')
        await sleep(500)
        check((await w()) > before, `${tag}: content widens when the sidebar is hidden`)
        await shot(`${tag}-sidebar-hidden`)
        await open('guides/rotate-keys/')
        await until(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'collapsed'`, 'the remembered sidebar state')
        check(await ev(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'collapsed'`), `${tag}: hidden sidebar is remembered`)
        await key('b', 2)
        await until(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'expanded'`, 'Ctrl-B', 3000)
        check(await ev(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'expanded'`), `${tag}: Ctrl-B shows the sidebar again`)
        for (const [name, path] of [['home', ''], ['concept', 'concepts/cni/'], ['cli', 'reference/cli/ichor_adopt/'], ['crd', 'reference/crds/taloscluster/'], ['adr-index', 'adr/']]) {
          await open(path)
          await shot(`${tag}-${name}`)
        }
      } else {
        // Phone: the sidebar is a drawer, "On this page" a collapsible above the article.
        await open('design/architecture/')
        await click('[data-slot=sidebar-trigger]')
        check(await until(`!!document.querySelector('[data-mobile=true]')`, 'the drawer'), `${tag}: the menu button opens the drawer`)
        await shot(`${tag}-drawer`)
        await key('Escape')
        await sleep(400)
        await ev(`[...document.querySelectorAll('button')].find((b) => b.textContent.includes('On this page'))?.click()`)
        await shot(`${tag}-toc`)
        check(await ev(`document.documentElement.scrollWidth <= window.innerWidth`), `${tag}: architecture page has no sideways scroll`)
      }
    }
  }
  ws.close()
} finally {
  chrome.kill()
  server.close()
}
if (failed) {
  console.error(`${failed} check(s) failed`)
  process.exit(1)
}
