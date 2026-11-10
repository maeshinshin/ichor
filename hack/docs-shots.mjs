// Drives the built docs site (site/dist) in headless Chromium (from the dev shell) over the
// DevTools protocol: serves dist under the Pages base, takes screenshots at desktop and phone
// width in light and dark, and checks search, the hideable sidebar, relative links, the Copy page
// menu (clipboard contents, the .md and llms routes), the heading links, the theme icon, the draw.io diagrams in both themes and that the
// phone layout does not scroll sideways. Exits 1 on a failed check. No npm dependency.
//
//   node hack/docs-shots.mjs <outDir> [distDir]     (make docs-check runs it after make docs-site)
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { extname, join, normalize } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

const [outDir, dist = 'site/dist-all'] = process.argv.slice(2)
if (!outDir) {
  console.error('usage: node hack/docs-shots.mjs <outDir> [distDir]')
  process.exit(2)
}
const root = (process.env.DOCS_BASE ?? '/ichor').replace(/\/*$/, '/')
const base = root + 'next/' // the pages under test; dist is the merged tree of all versions (make docs-site-all)
const fakeNewer = { on: false } // serve a versions.json that lists a newer release, to see the old-version banner
mkdirSync(outDir, { recursive: true })

const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.svg': 'image/svg+xml', '.wasm': 'application/wasm', '.md': 'text/markdown', '.txt': 'text/plain' }
const server = createServer((req, res) => {
  const path = decodeURIComponent(new URL(req.url, 'http://x').pathname)
  if (!path.startsWith(root)) return void res.writeHead(404).end()
  let file = join(dist, normalize(path.slice(root.length)))
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html')
  if (!existsSync(file)) return void res.writeHead(404).end()
  if (fakeNewer.on && path === root + 'versions.json') {
    const vs = JSON.parse(readFileSync(file, 'utf8')).map((v) => ({ ...v, isLatest: false }))
    vs.push({ name: 'v9.9.9', path: 'v9.9.9/', isLatest: true, isNext: false })
    return void res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(vs))
  }
  res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream' }).end(readFileSync(file))
})
await new Promise((r) => server.listen(0, '127.0.0.1', r))
const origin = `http://127.0.0.1:${server.address().port}`

const port = 9300 + Math.floor(Math.random() * 500)
const chrome = spawn(
  process.env.CHROMIUM ?? 'chromium',
  ['--headless=new', '--no-sandbox', '--disable-gpu', '--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4', `--remote-debugging-port=${port}`, '--user-data-dir=' + join(outDir, '.profile'), 'about:blank'],
  { stdio: 'ignore' },
)
const diagrams = [['architecture-home', ''], ['architecture', 'design/architecture/'], ['machine-lifecycle', 'concepts/clusters-and-machines/'], ['ca-rotation', 'concepts/ca-and-key-rotation/'], ['roles', 'concepts/security/']]
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
  await send('Browser.grantPermissions', { origin, permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'] })

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
  // A real mouse click (or just a move) at the middle of the element `expr` evaluates to; Radix menus open on pointerdown.
  const mouse = async (expr, type = 'click') => {
    const p = await ev(`(() => { const e = ${expr}; if (!e) return null; e.scrollIntoView({ block: 'center' }); const r = e.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 } })()`)
    if (!p) return false
    await s('Input.dispatchMouseEvent', { type: 'mouseMoved', ...p })
    if (type === 'click') for (const t of ['mousePressed', 'mouseReleased']) await s('Input.dispatchMouseEvent', { type: t, ...p, button: 'left', buttons: t === 'mousePressed' ? 1 : 0, clickCount: 1 })
    return true
  }
  const clipboard = () => ev(`navigator.clipboard.readText()`)
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

      // The theme icon is chosen by CSS from the `dark` class, so the first paint already shows the right one.
      const shownIcon = await ev(`(() => { const v = (n) => getComputedStyle(document.querySelector('header .lucide-' + n)).display !== 'none'; return v('sun') ? 'sun' : v('moon') ? 'moon' : '' })()`)
      check(shownIcon === (theme === 'dark' ? 'sun' : 'moon'), `${tag}: the theme toggle shows the ${theme === 'dark' ? 'sun' : 'moon'} icon (${shownIcon})`)
      check(/lucide-sun[^"]*hidden dark:block/.test(await (await fetch(origin + base + 'guides/rotate-keys/')).text()), `${tag}: the icon swap is in the server-rendered HTML`)

      // Copy page: the menu, the Markdown on the clipboard, the static .md and llms routes.
      const title = await ev(`document.querySelector('article h1').textContent`)
      const md = await fetch(origin + base + 'guides/rotate-keys.md')
      const mdText = await md.text()
      check(md.status === 200 && mdText.startsWith(`# ${title}`), `${tag}: guides/rotate-keys.md is served and starts with "# ${title}"`)
      const prose = mdText.replace(/^```[\s\S]*?^```/gm, '').replace(/`[^`]*`/g, '')
      check(!/\]\((?!https?:)[^#)]/.test(prose), `${tag}: the Markdown has no relative links`)
      for (const f of ['index.md', 'llms.txt', 'llms-full.txt']) check((await fetch(origin + base + f)).status === 200, `${tag}: ${f} returns 200`)
      check((await (await fetch(origin + base + 'llms.txt')).text()).includes('/guides/rotate-keys.md)'), `${tag}: llms.txt links the page's .md`)
      const item = (text) => `[...document.querySelectorAll('[data-slot=dropdown-menu-item]')].find((e) => e.textContent.includes(${JSON.stringify(text)}))`
      const more = `document.querySelector('button[aria-label="More page actions"]')`
      const menuOpen = () => until(`document.querySelectorAll('[data-slot=dropdown-menu-item]').length >= 3`, 'the Copy page menu', 3000)
      check(await ev(`!![...document.querySelectorAll('article button')].find((b) => b.textContent.trim() === 'Copy page')`), `${tag}: the Copy page button exists`)
      await mouse(more)
      check(await menuOpen(), `${tag}: the Copy page menu opens`)
      check(await ev(`['Copy as Markdown', 'View as Markdown', 'Copy link'].every((t) => [...document.querySelectorAll('[data-slot=dropdown-menu-item]')].some((e) => e.textContent.includes(t)))`), `${tag}: the menu lists Copy as Markdown, View as Markdown and Copy link`)
      await shot(`${tag}-page-menu`)
      await mouse(item('Copy as Markdown'))
      await until(`!!document.querySelector('[data-sonner-toast]')`, 'the toast', 3000)
      check(await ev(`!!document.querySelector('[data-sonner-toast]')`), `${tag}: a toast confirms the copy`)
      const copied = await clipboard()
      check(copied.startsWith(`# ${title}`) && copied === mdText, `${tag}: Copy as Markdown puts the page's Markdown (starting "# ${title}") on the clipboard`)
      await mouse(more)
      await menuOpen()
      check(await ev(`${item('View as Markdown')}?.getAttribute('href') === '${base}guides/rotate-keys.md'`), `${tag}: View as Markdown links to the .md`)
      check(!(await ev(`!!${item('Share')}`)), `${tag}: no Share item without the Web Share API`)
      await mouse(item('Copy link'))
      await sleep(300)
      check((await clipboard()) === (process.env.DOCS_SITE ?? 'https://ichor.maesh.dev') + base + 'guides/rotate-keys/', `${tag}: Copy link copies the canonical URL`)

      // Heading links: hidden until hover or focus, copy the URL with the anchor and move the address bar.
      const anchor = `document.querySelector('article h2 a[data-heading-link]')`
      const opacity = () => ev(`getComputedStyle(${anchor}).opacity`)
      if (!mobile) {
        await mouse(`document.querySelector('article h1')`, 'move')
        await sleep(400)
        const idle = await opacity()
        check(idle === '0', `${tag}: heading links are hidden until hover (opacity ${idle})`)
        await mouse(`${anchor}.parentElement`, 'move')
        await sleep(300)
        check((await opacity()) === '1', `${tag}: a heading link shows on hover`)
        await shot(`${tag}-heading-hover`)
      }
      await ev(`${anchor}.focus()`)
      await sleep(300)
      check((await opacity()) === '1', `${tag}: a heading link shows on keyboard focus`)
      const hash = await ev(`${anchor}.getAttribute('href')`)
      await mouse(anchor)
      await sleep(400)
      check((await ev(`location.hash`)) === hash, `${tag}: clicking a heading link updates the address bar (${hash})`)
      check((await clipboard()) === `${origin}${base}guides/rotate-keys/${hash}`, `${tag}: clicking a heading link copies the URL with ${hash}`)
      await shot(`${tag}-heading-link`)

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

      // A code block's copy button (hovered), and what it copies.
      await mouse(`document.querySelector('article pre + div button')`, 'move')
      await sleep(300)
      await shot(`${tag}-codeblock`)
      await mouse(`document.querySelector('article pre + div button')`)
      await sleep(300)
      check((await clipboard()) === (await ev(`document.querySelector('article pre code').textContent`)), `${tag}: the code copy button copies the code`)

      // Syntax highlighting: a YAML block has several token colours, taken from the theme that is on.
      await ev(`(() => { const p = [...document.querySelectorAll('article pre')].find((p) => p.textContent.includes('kind: InitConfig')); p.scrollIntoView({ block: 'center' }) })()`)
      await sleep(300)
      const colours = await ev(`new Set([...[...document.querySelectorAll('article pre.astro-code')].find((p) => p.textContent.includes('kind: InitConfig')).querySelectorAll('span')].map((s) => getComputedStyle(s).color)).size`)
      check(colours > 2, `${tag}: a YAML block is highlighted (${colours} colours)`)
      await shot(`${tag}-highlight`)

      // Versions: versions.json, the redirects, the switcher, the banners and a per-version search.
      const vs = await (await fetch(origin + root + 'versions.json')).json()
      const latest = vs.find((v) => v.isLatest)
      check(vs[0]?.name === 'next' && vs[0].isNext && vs.filter((v) => v.isLatest).length === 1, `${tag}: versions.json lists next and exactly one latest (${vs.map((v) => v.name)})`)
      check((await (await fetch(origin + root)).text()).includes(`url=${root}${latest.path}`), `${tag}: the site root redirects to ${latest.name}`)
      check((await fetch(origin + root + 'latest/guides/getting-started/')).status === 200, `${tag}: latest/ has a redirect for each page`)
      check((await fetch(origin + root + latest.path + 'pagefind/pagefind.js')).status === 200, `${tag}: ${latest.name} has its own search index`)
      const inMenu = (text) => `[...document.querySelectorAll('[data-slot=dropdown-menu-radio-item]')].find((e) => e.textContent.includes(${JSON.stringify(text)}))`
      const switcher = `[...document.querySelectorAll('button[aria-label="Documentation version"]')].find((b) => b.getClientRects().length)`
      await open('guides/getting-started/')
      check(await ev(`document.querySelector('article [role=alert]')?.textContent.includes('Unreleased (main)')`), `${tag}: next shows the "Unreleased (main)" banner`)
      await mouse(switcher)
      check(await until(`!!${inMenu('v')}`, 'the version menu', 3000), `${tag}: the version switcher opens`)
      check(await ev(`!!${inMenu('next (main)')} && !!${inMenu(`${latest.name} (${latest.patch}) (latest)`)}`), `${tag}: the switcher lists next (main) and ${latest.name} (${latest.patch}) (latest)`)
      await shot(`${tag}-version-switcher`)
      await mouse(inMenu(latest.name))
      await until(`location.pathname === '${root}${latest.path}guides/getting-started/'`, 'the same page in the release', 5000)
      check(await ev(`location.pathname`) === `${root}${latest.path}guides/getting-started/`, `${tag}: switching keeps the page path when it exists in ${latest.name}`)
      await ready()
      check(!(await ev(`!!document.querySelector('article [role=alert]')`)), `${tag}: the newest release has no banner`)
      await open('design/architecture/')
      const there = (await fetch(origin + root + latest.path + 'design/architecture/', { method: 'HEAD' })).ok
      await mouse(switcher)
      await until(`!!${inMenu('v')}`, 'the version menu', 3000)
      await mouse(inMenu(latest.name))
      await until(`location.pathname.startsWith('${root}${latest.path}')`, 'the release', 5000)
      check(await ev(`location.pathname`) === `${root}${latest.path}${there ? 'design/architecture/' : ''}`, `${tag}: switching to a page ${latest.name} lacks goes to its overview (page there: ${there})`)
      await ready()
      await key('k', 2)
      await until(`!!document.querySelector('[data-slot=command-input]')`, 'the search dialog', 3000)
      await s('Input.insertText', { text: 'adopt' })
      await until(`document.querySelectorAll('[data-slot=command-item]').length > 0`, 'search results')
      await key('Enter')
      await until(`!location.pathname.endsWith('${root}${latest.path}')`, 'a search result', 5000)
      check((await ev(`location.pathname`)).startsWith(`${root}${latest.path}`), `${tag}: search in ${latest.name} stays in that version`)
      await ready()
      fakeNewer.on = true // a release newer than this build exists: the old-version banner
      await s('Page.navigate', { url: `${origin}${root}${latest.path}guides/getting-started/` })
      await sleep(300)
      await ready()
      await until(`!!document.querySelector('article [role=alert]')`, 'the old-version banner', 3000)
      const banner = await ev(`document.querySelector('article [role=alert]')?.textContent`)
      check(banner?.includes(`You are viewing the docs for ${latest.name}`) && banner.includes('The latest is v9.9.9'), `${tag}: an old version warns "${banner}"`)
      await shot(`${tag}-old-version-banner`)
      await open('guides/getting-started/')
      await until(`document.querySelector('article [role=alert]')?.textContent.includes('v9.9.9')`, 'the latest release in the next banner', 3000)
      check(await ev(`document.querySelector('article [role=alert]')?.textContent.includes('The latest release is v9.9.9')`), `${tag}: next's banner names the latest release`)
      fakeNewer.on = false

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
        // Code blocks get a copy button and GitHub alerts render as the preactcn Alert.
        await open('contributing-docs/')
        await until(`!!document.querySelector('article pre + div [aria-label="Copy code"]')`, 'the copy button', 3000)
        check(await ev(`!!document.querySelector('article pre + div [aria-label="Copy code"]')`), `${tag}: code blocks have a copy button`)
        check(await ev(`!!document.querySelector('article [data-slot=alert]')`), `${tag}: a GitHub alert renders as an Alert`)
        await shot(`${tag}-contributing`)
        for (const [name, path] of [['home', ''], ['concept', 'concepts/cni/'], ['cli', 'reference/cli/ichor_adopt/'], ['crd', 'reference/crds/taloscluster/']]) {
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

      // Diagrams (docs/images/*.drawio.svg): the page names the light image, the site adds the dark one and shows the
      // one for the theme. Both must load, only one may be visible, and the page must not scroll sideways.
      for (const [name, path] of diagrams) {
        await open(path)
        const imgs = `[...document.querySelectorAll('article img[src*=".drawio.svg"]')]`
        const shown = `${imgs}.filter((i) => i.getClientRects().length)`
        await until(`${imgs}.length === 2 && ${imgs}.every((i) => i.complete)`, `the ${name} images`, 5000)
        check(await ev(`${imgs}.length === 2 && ${shown}.length === 1`), `${tag}: ${name} has a light and a dark image, one visible`)
        check(await ev(`${shown}[0]?.src.endsWith('.${theme}.drawio.svg') && ${shown}[0].naturalWidth > 0`), `${tag}: ${name} shows its ${theme} image, loaded`)
        check(await ev(`(${shown}[0]?.alt ?? '').length > 40 && !!${shown}[0].closest('p')?.nextElementSibling?.textContent.trim()`), `${tag}: ${name} has alt text and a caption`)
        check(await ev(`document.documentElement.scrollWidth <= window.innerWidth`), `${tag}: ${name} page has no sideways scroll`)
        await ev(`${shown}[0].scrollIntoView({ block: 'center' })`)
        await shot(`${tag}-diagram-${name}`)
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
