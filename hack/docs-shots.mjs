// Drives the built docs site (site/dist) in headless Chromium (from the dev shell) over the
// DevTools protocol: serves dist under the Pages base, takes screenshots at desktop and phone
// width in light and dark, and checks search, the hideable sidebar, relative links, the Copy page
// menu (clipboard contents, the .md and llms routes), the heading links, the theme icon, the draw.io diagrams in both themes and that the
// phone layout does not scroll sideways. Exits 1 on a failed check. No npm dependency.
//
//   node hack/docs-shots.mjs <outDir> [distDir]     (make docs-check runs it after make docs-site)
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { extname, join, normalize } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

const [outDir, dist = 'site/dist-all'] = process.argv.slice(2)
if (!outDir) {
  console.error('usage: node hack/docs-shots.mjs <outDir> [distDir]')
  process.exit(2)
}
const root = (process.env.DOCS_BASE ?? '/').replace(/\/*$/, '/')
const base = root + 'next/' // the pages under test; dist is the merged tree of all versions (make docs-site-all)
const fakeNewer = { on: false } // serve a versions.json that lists a newer release, to see the old-version banner
mkdirSync(outDir, { recursive: true })

// The pages the checks open, relative to base. Each is asserted on by its content (the search target, the links,
// the phone drawer, the code and alert checks) or is a screenshot, so the routes are listed here once. A route that
// is not in the built next tree stops the run before the browser starts; the diagram pages are found in the HTML.
const at = {
  home: '',
  rotateKeys: 'guides/rotate-keys/',
  gettingStarted: 'guides/getting-started/',
  createCluster: 'guides/getting-started/create-the-cluster/vip/', // has the YAML blocks the code checks use
  architecture: 'design/architecture/',
  contributing: 'development/contributing-docs/',
  concept: 'concepts/cni/',
  cli: 'reference/cli/ichor_adopt/',
  crd: 'reference/crds/taloscluster/',
}
const mdOf = (route) => (route ? route.replace(/\/$/, '') : 'index') + '.md'
const built = [] // every route of dist/next, '' for the home page, else 'a/b/' for a directory with an index.html
const walk = (dir, route) => {
  if (existsSync(join(dir, 'index.html'))) built.push(route)
  for (const e of readdirSync(dir, { withFileTypes: true })) if (e.isDirectory()) walk(join(dir, e.name), `${route}${e.name}/`)
}
if (!existsSync(join(dist, 'next'))) {
  console.error(`docs-shots: no next version in ${dist}; run make docs-site-all (or docs-site) first`)
  process.exit(1)
}
walk(join(dist, 'next'), '')
const missing = Object.entries(at).filter(([, route]) => !built.includes(route))
for (const [key, route] of missing) console.error(`docs-shots: no page at route "${route}" (at.${key} in hack/docs-shots.mjs): the page moved or was removed, so point at.${key} at its new route`)
if (missing.length) process.exit(1)
const diagrams = built.filter((route) => /<img\b[^>]*\.drawio\.svg/.test(readFileSync(join(dist, 'next', route, 'index.html'), 'utf8')))
if (!diagrams.length) {
  console.error(`docs-shots: no page of ${dist}/next embeds a .drawio.svg image`)
  process.exit(1)
}

// The doc pages: the built routes that render an article (not the version or search pages), each of which the sidebar must list.
const docRoutes = built.filter((route) => readFileSync(join(dist, 'next', route, 'index.html'), 'utf8').includes('<article'))
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
let failed = 0
const check = (ok, what) => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${what}`)
  if (!ok) failed++
}

try {
  let version
  for (let i = 0; i < 300 && !version; i++) { // 30 s: a bound for a loaded machine, not an expected time
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
  // A navigation replaces the document, so an evaluate it interrupts fails with "navigated or closed". Counting the
  // loads lets a wait for "the next load" see one that finished before the wait began.
  let loads = 0
  ws.onmessage = (m) => {
    const d = JSON.parse(m.data)
    if (d.id && pending.has(d.id)) pending.get(d.id)(d)
    if (d.method === 'Page.loadEventFired') loads++
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

  const loadedSince = async (since) => {
    for (const end = Date.now() + 30000; loads === since; await sleep(20)) if (Date.now() > end) throw new Error('the page did not finish loading within 30 s')
  }
  // An evaluate that a navigation interrupts is sent again once the page that replaces it has loaded.
  const ev = async (expression) => {
    for (;;) {
      const since = loads
      try {
        return (await s('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })).result?.value
      } catch (e) {
        if (!/navigated or closed/.test(e.message)) throw e
        await loadedSince(since)
      }
    }
  }
  // Every wait polls for the state it needs and gives up only after `ms`, which is a bound for a loaded machine and not an
  // expected time: a wait that succeeds costs what the page takes, so the bound can be generous.
  const until = async (expression, what, ms = 20000) => {
    for (const end = Date.now() + ms; Date.now() < end; await sleep(100)) if (await ev(expression)) return true
    check(false, `timed out waiting for ${what}`)
    return false
  }
  // Preact runs a component's effects, which is where the keyboard shortcuts are attached and where state read from storage
  // is applied, after the next frame, not when the island has hydrated or its DOM has changed. A key pressed before that
  // is lost (no listener yet, or the listener of the render before). A frame and a task after it is when they have run, and
  // twice because an effect that sets state renders again and queues effects of its own.
  const idle = async () => {
    for (let i = 0; i < 2; i++) await ev(`new Promise((r) => requestAnimationFrame(() => setTimeout(r)))`)
  }
  // Hydrated: every island has dropped its ssr attribute and its effects have run.
  const ready = async () => {
    const ok = await until(`document.readyState === 'complete' && !document.querySelector('astro-island[ssr]')`, 'hydration')
    await idle()
    return ok
  }
  const shot = async (name) => {
    await sleep(400) // lets a transition finish for the picture; nothing is checked against it
    const { data } = await s('Page.captureScreenshot', { format: 'png' })
    const file = join(outDir, `${name}.png`)
    writeFileSync(file, Buffer.from(data, 'base64'))
    console.log(`shot ${file}`)
  }
  // Page.navigate returns before the new page exists: wait for its load before anything reads the page.
  const go = async (url) => {
    const since = loads
    const { errorText } = await s('Page.navigate', { url })
    if (errorText) throw new Error(`navigating to ${url}: ${errorText}`)
    await loadedSince(since)
  }
  const open = async (path) => {
    await go(origin + base + path)
    await ready()
  }
  const click = (selector) => ev(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if (e) e.click(); return !!e })()`)
  // A real mouse click (or just a move) at the middle of the element `expr` evaluates to; Radix menus open on pointerdown.
  // The point is taken only once the element has stood still for 10 frames and is what the browser hit-tests at that point:
  // a banner that grows after load, or a menu that is still opening or closing, moves things, and a click at the old
  // position then lands on another element (the previous menu item, or nothing). An element that never settles or stays
  // covered is reported, with what covers it, and not clicked.
  const mouse = async (expr, type = 'click') => {
    const p = await ev(`new Promise((resolve) => {
      const e = ${expr}
      if (!e) return resolve(null)
      e.scrollIntoView({ block: 'center' })
      const at = () => { const r = e.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2, w: r.width, h: r.height } }
      let last = at(), still = 0, frames = 0
      const tick = () => {
        const now = at()
        still = now.x === last.x && now.y === last.y && now.w === last.w && now.h === last.h ? still + 1 : 0
        last = now
        const top = document.elementFromPoint(now.x, now.y)
        const hit = !!top && e.isConnected && (e === top || e.contains(top))
        if (still >= 10 && hit) return resolve({ x: now.x, y: now.y })
        if (++frames > 180) return resolve({ covered: top ? top.tagName + (top.getAttribute('aria-label') ? '[' + top.getAttribute('aria-label') + ']' : '') : 'nothing' })
        requestAnimationFrame(tick)
      }
      requestAnimationFrame(tick)
    })`)
    if (!p) return false
    if (p.covered) {
      console.log(`docs-shots: not clicking ${expr.slice(0, 80)}: it never stood still and uncovered, the point is on ${p.covered}`)
      return false
    }
    await s('Input.dispatchMouseEvent', { type: 'mouseMoved', ...p })
    if (type === 'click') for (const t of ['mousePressed', 'mouseReleased']) await s('Input.dispatchMouseEvent', { type: t, ...p, button: 'left', buttons: t === 'mousePressed' ? 1 : 0, clickCount: 1 })
    return true
  }
  // Ctrl-K, the text, and the results with one selected: the dialog opens, the input takes the focus (the text goes to the
  // focused element, so before that it is lost), the Pagefind index answers, and Enter opens the selected result (before one
  // is selected it does nothing).
  const search = async (text) => {
    const opened = await press('k', 2, `!!document.querySelector('[data-slot=command-input]')`, 'the search dialog')
    if (!opened) return false
    await until(`document.activeElement?.matches('[data-slot=command-input]')`, 'the search input to have the focus')
    await s('Input.insertText', { text })
    await until(`!!document.querySelector('[data-slot=command-item][aria-selected=true]')`, 'search results with one selected')
    return opened
  }
  // A key that nothing receives changes nothing, so a press whose effect has not shown after 2 s is sent again (twice at most):
  // there is no state it could undo. Each repeat is reported, so a shortcut that keeps needing one shows in the log. When the
  // last press still has no effect the check fails and says what had the focus.
  const press = async (k, modifiers, expression, what) => {
    for (let attempt = 1; attempt <= 3; attempt++) {
      await key(k, modifiers)
      for (const end = Date.now() + (attempt < 3 ? 2000 : 20000); Date.now() < end; await sleep(100)) {
        if (await ev(expression)) {
          if (attempt > 1) console.log(`note: ${what} needed ${attempt} presses of ${k}`)
          return true
        }
      }
    }
    const state = await ev(`JSON.stringify({ hasFocus: document.hasFocus(), visible: document.visibilityState, active: document.activeElement?.tagName, url: location.pathname })`)
    check(false, `timed out waiting for ${what} after 3 presses of ${k} (${state})`)
    return false
  }
  const clipboard = () => ev(`navigator.clipboard.readText()`)
  // The clipboard is written, and a CSS transition ends, some time after the click or focus that causes it, so a fixed
  // sleep races them on a slow runner. Read until the value is `want` or 5 s pass, and return the last value read:
  // the check still compares it with `want`, so a value that never arrives fails and says what was there instead.
  const eventually = async (read, want) => {
    let got = await read()
    for (const end = Date.now() + 20000; got !== want && Date.now() < end; got = await read()) await sleep(100)
    return got
  }
  const clipboardBecomes = (want) => eventually(clipboard, want)
  // The chevron turns by the transform or, for Tailwind v4 rotate-* classes, by the rotate property: read both.
  const turnOf = (el) => `(() => { const s = getComputedStyle(${el}); return s.transform + " " + s.rotate })()`
  // A disclosure: toggling it changes aria-expanded and turns its chevron. Each is checked on its own, since the chevron
  // is what sighted readers see and aria-expanded is what assistive technology reads, and either can be right while the
  // other is not. `read` gives [aria-expanded, the chevron's computed transform] of the trigger.
  const disclosure = async (tag, name, trigger) => {
    const read = `(() => { const b = ${trigger}; return b ? [b.getAttribute('aria-expanded'), ${turnOf("b.querySelector('svg')")}] : ['missing', 'missing'] })()`
    const settle = async (done) => {
      let v = await ev(read)
      for (let i = 0; i < 30 && !done(v); i++) {
        await sleep(100)
        v = await ev(read)
      }
      return v
    }
    const [e0, t0] = await ev(read)
    await ev(`${trigger}.click()`)
    const [e1] = await settle(([e]) => e !== e0)
    const [, t1] = await settle(([, t]) => t !== t0)
    check(e1 !== e0, `${tag}: ${name} sets aria-expanded from ${e0} to ${e1} when opened`)
    check(t1 !== t0, `${tag}: ${name}'s chevron turns when opened (transform ${t0} to ${t1})`)
    await ev(`${trigger}.click()`)
    const [e2] = await settle(([e]) => e === e0)
    const [, t2] = await settle(([, t]) => t === t0)
    check(e2 === e0, `${tag}: ${name} sets aria-expanded back to ${e0} when closed`)
    check(t2 === t0, `${tag}: ${name}'s chevron turns back when closed (transform ${t2})`)
  }
  const key = async (k, modifiers = 0) => {
    await idle()
    for (const type of ['keyDown', 'keyUp']) await s('Input.dispatchKeyEvent', { type, key: k, code: `Key${k.toUpperCase()}`, modifiers })
  }

  for (const [size, width, height, mobile] of [['desktop', 1280, 800, false], ['phone', 375, 812, true]]) {
    for (const theme of ['light', 'dark']) {
      const tag = `${size}-${theme}`
      await s('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile })
      await s('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] })
      await go(origin + base)
      await ev('localStorage.clear()')

      await open(at.rotateKeys)
      check((await ev(`document.documentElement.classList.contains('dark')`)) === (theme === 'dark'), `${tag}: follows the system theme`)
      check(await ev(`document.documentElement.scrollWidth <= window.innerWidth`), `${tag}: no sideways scroll`)
      await shot(`${tag}-page`)
      if (!mobile) {
        // Every doc page is listed in the sidebar and visible there with no click: a section that folds its pages away fails here.
        const listed = await ev(`[...document.querySelectorAll('[data-slot=sidebar] a[href]')].filter((a) => a.getClientRects().length).map((a) => a.pathname)`)
        const unlisted = docRoutes.filter((route) => !listed.includes(base + route))
        check(unlisted.length === 0, `${tag}: every page is in the sidebar without a click (not listed: ${unlisted.join(' ') || 'none'})`)
      }

      // The theme icon is chosen by CSS from the `dark` class, so the first paint already shows the right one.
      const shownIcon = await ev(`(() => { const v = (n) => getComputedStyle(document.querySelector('header .lucide-' + n)).display !== 'none'; return v('sun') ? 'sun' : v('moon') ? 'moon' : '' })()`)
      check(shownIcon === (theme === 'dark' ? 'sun' : 'moon'), `${tag}: the theme toggle shows the ${theme === 'dark' ? 'sun' : 'moon'} icon (${shownIcon})`)
      check(/lucide-sun[^"]*hidden dark:block/.test(await (await fetch(origin + base + at.rotateKeys)).text()), `${tag}: the icon swap is in the server-rendered HTML`)

      // Copy page: the menu, the Markdown on the clipboard, the static .md and llms routes.
      const title = await ev(`document.querySelector('article h1').textContent`)
      const md = await fetch(origin + base + mdOf(at.rotateKeys))
      const mdText = await md.text()
      check(md.status === 200 && mdText.startsWith(`# ${title}`), `${tag}: ${mdOf(at.rotateKeys)} is served and starts with "# ${title}"`)
      const prose = mdText.replace(/^```[\s\S]*?^```/gm, '').replace(/`[^`]*`/g, '')
      check(!/\]\((?!https?:)[^#)]/.test(prose), `${tag}: the Markdown has no relative links`)
      for (const f of ['index.md', 'llms.txt', 'llms-full.txt']) check((await fetch(origin + base + f)).status === 200, `${tag}: ${f} returns 200`)
      check((await (await fetch(origin + base + 'llms.txt')).text()).includes('/' + mdOf(at.rotateKeys) + ')'), `${tag}: llms.txt links the page's .md`)
      const item = (text) => `[...document.querySelectorAll('[data-slot=dropdown-menu-item]')].find((e) => e.textContent.includes(${JSON.stringify(text)}))`
      const more = `document.querySelector('button[aria-label="More page actions"]')`
      // Open means the Radix content is in its open state: a menu that is closing still has its items in the page.
      const menuContent = `document.querySelector('[data-slot=dropdown-menu-content]')`
      const menuOpen = () => until(`${menuContent}?.dataset.state === 'open' && document.querySelectorAll('[data-slot=dropdown-menu-item]').length >= 3`, 'the Copy page menu')
      const menuClosed = () => until(`!${menuContent}`, 'the Copy page menu to close')
      check(await ev(`!![...document.querySelectorAll('article button')].find((b) => b.textContent.trim() === 'Copy page')`), `${tag}: the Copy page button exists`)
      const moreState = `(() => { const b = ${more}; return [b.getAttribute('aria-expanded'), ${turnOf("b.querySelector('svg')")}] })()`
      const [closedExpanded, closedTurn] = await ev(moreState)
      await mouse(more)
      check(await menuOpen(), `${tag}: the Copy page menu opens`)
      // The menu is a disclosure too: its chevron turns and aria-expanded is set while it is open (the menu stays open below).
      await until(`${turnOf(`${more}.querySelector('svg')`)} !== ${JSON.stringify(closedTurn)}`, 'the More page actions chevron to turn', 3000)
      const [openedExpanded, openedTurn] = await ev(moreState)
      check(openedExpanded === 'true' && openedExpanded !== closedExpanded, `${tag}: More page actions sets aria-expanded to true when open (was ${closedExpanded}, now ${openedExpanded})`)
      check(openedTurn !== closedTurn, `${tag}: More page actions' chevron turns when open (transform ${closedTurn} to ${openedTurn})`)
      check(await ev(`['Copy as Markdown', 'View as Markdown', 'Copy link'].every((t) => [...document.querySelectorAll('[data-slot=dropdown-menu-item]')].some((e) => e.textContent.includes(t)))`), `${tag}: the menu lists Copy as Markdown, View as Markdown and Copy link`)
      await shot(`${tag}-page-menu`)
      await mouse(item('Copy as Markdown'))
      await until(`!!document.querySelector('[data-sonner-toast]')`, 'the toast')
      check(await ev(`!!document.querySelector('[data-sonner-toast]')`), `${tag}: a toast confirms the copy`)
      const copied = await clipboard()
      check(copied.startsWith(`# ${title}`) && copied === mdText, `${tag}: Copy as Markdown puts the page's Markdown (starting "# ${title}") on the clipboard`)
      await menuClosed()
      await mouse(more)
      check(await menuOpen(), `${tag}: the Copy page menu opens again`)
      check(await ev(`${item('View as Markdown')}?.getAttribute('href') === '${base}${mdOf(at.rotateKeys)}'`), `${tag}: View as Markdown links to the .md`)
      check(!(await ev(`!!${item('Share')}`)), `${tag}: no Share item without the Web Share API`)
      const canonical = (process.env.DOCS_SITE ?? 'https://ichor.maesh.dev') + base + at.rotateKeys
      // Record what the click lands on, so a failure says whether it hit another item, nothing, or the right item and no copy followed.
      await ev(`window.__clicks = []; document.addEventListener('click', (e) => window.__clicks.push((e.target.closest('[data-slot=dropdown-menu-item]') ?? e.target).textContent.trim().slice(0, 30) || e.target.tagName), { capture: true })`)
      const clicked = await mouse(item('Copy link'))
      const linked = await clipboardBecomes(canonical)
      check(linked === canonical, `${tag}: Copy link copies the canonical URL (sent: ${clicked}, click landed on: ${await ev(`JSON.stringify(window.__clicks)`)}, clipboard: ${JSON.stringify(linked.slice(0, 80))})`)
      // Closing the menu gives the focus back to its trigger; let that finish before the heading link below takes the focus.
      await menuClosed()

      // Heading links: hidden until hover or focus, copy the URL with the anchor and move the address bar.
      const anchor = `document.querySelector('article h2 a[data-heading-link]')`
      const opacity = () => ev(`getComputedStyle(${anchor}).opacity`)
      if (!mobile) {
        await mouse(`document.querySelector('article h1')`, 'move')
        const hidden = await eventually(opacity, '0') // the fade runs after the pointer has moved
        check(hidden === '0', `${tag}: heading links are hidden until hover (opacity ${hidden})`)
        await mouse(`${anchor}.parentElement`, 'move')
        check((await eventually(opacity, '1')) === '1', `${tag}: a heading link shows on hover`)
        await shot(`${tag}-heading-hover`)
      }
      await ev(`${anchor}.focus()`)
      const focused = await eventually(opacity, '1')
      check(focused === '1', `${tag}: a heading link shows on keyboard focus (opacity ${focused})`)
      const hash = await ev(`${anchor}.getAttribute('href')`)
      await mouse(anchor)
      await until(`location.hash === ${JSON.stringify(hash)}`, 'the address bar to take the heading link')
      check((await ev(`location.hash`)) === hash, `${tag}: clicking a heading link updates the address bar (${hash})`)
      check((await clipboardBecomes(`${origin}${base}${at.rotateKeys}${hash}`)) === `${origin}${base}${at.rotateKeys}${hash}`, `${tag}: clicking a heading link copies the URL with ${hash}`)
      await shot(`${tag}-heading-link`)

      // Search: the Pagefind index answers "rotate" with the key-rotation guide.
      check(await search('rotate'), `${tag}: Ctrl-K opens search`)
      check((await ev(`document.querySelectorAll('[data-slot=command-item]').length`)) > 0, `${tag}: search for "rotate" has results`)
      await shot(`${tag}-search`)
      await key('Enter')
      check(await until(`location.pathname.startsWith(${JSON.stringify(base)}) && !location.pathname.endsWith('/${at.rotateKeys}') || location.hash !== ''`, 'navigation from a result'), `${tag}: Enter opens the result`)
      await ready()

      // Relative links between Markdown files became site URLs that resolve.
      await open(at.gettingStarted)
      const links = await ev(`[...document.querySelectorAll('article a[href^="${base}"]')].map((a) => a.getAttribute('href').split('#')[0])`)
      const bad = []
      for (const l of [...new Set(links)]) if ((await fetch(origin + l)).status !== 200) bad.push(l)
      check(links.length > 0 && !bad.length, `${tag}: ${links.length} internal links in getting-started, ${bad.length} broken ${bad.join(' ')}`)

      // A code block's copy button (hovered), and what it copies.
      await open(at.createCluster)
      await mouse(`document.querySelector('article pre + div button')`, 'move')
      await shot(`${tag}-codeblock`)
      await mouse(`document.querySelector('article pre + div button')`)
      const code = await ev(`document.querySelector('article pre code').textContent`)
      check((await clipboardBecomes(code)) === code, `${tag}: the code copy button copies the code`)

      // Syntax highlighting: a YAML block has several token colours, taken from the theme that is on.
      await ev(`(() => { const p = [...document.querySelectorAll('article pre')].find((p) => p.textContent.includes('kind: InitConfig')); p.scrollIntoView({ block: 'center' }) })()`)
      const colours = await ev(`new Set([...[...document.querySelectorAll('article pre.astro-code')].find((p) => p.textContent.includes('kind: InitConfig')).querySelectorAll('span')].map((s) => getComputedStyle(s).color)).size`)
      check(colours > 2, `${tag}: a YAML block is highlighted (${colours} colours)`)
      await shot(`${tag}-highlight`)

      // Versions: versions.json, the redirects, the switcher, the banners and a per-version search.
      const vs = await (await fetch(origin + root + 'versions.json')).json()
      const latest = vs.find((v) => v.isLatest)
      check(vs[0]?.name === 'next' && vs[0].isNext && vs.filter((v) => v.isLatest).length === 1, `${tag}: versions.json lists next and exactly one latest (${vs.map((v) => v.name)})`)
      check((await (await fetch(origin + root)).text()).includes(`url=${root}${latest.path}`), `${tag}: the site root redirects to ${latest.name}`)
      check((await fetch(origin + root + 'latest/' + at.gettingStarted)).status === 200, `${tag}: latest/ has a redirect for each page`)
      check((await fetch(origin + root + latest.path + 'pagefind/pagefind.js')).status === 200, `${tag}: ${latest.name} has its own search index`)

      // A page must not move once it has loaded. A banner, or any component, that fills in after a fetch pushes everything
      // below it away, and a reader who taps as the page settles hits the wrong thing. The page as the server sent it (no
      // script runs) and the page once it is hydrated and versions.json is in must put the heading in the same place.
      const older = vs.find((v) => !v.isLatest && !v.isNext)
      const headingY = () => ev(`document.querySelector('article h1').getBoundingClientRect().y`)
      for (const path of [base + at.rotateKeys, root + latest.path + at.rotateKeys, older && root + older.path + at.rotateKeys].filter(Boolean)) {
        await s('Emulation.setScriptExecutionDisabled', { value: true })
        await go(origin + path)
        const served = await headingY()
        await s('Emulation.setScriptExecutionDisabled', { value: false })
        await go(origin + path)
        await ready()
        await until(`performance.getEntriesByType('resource').some((r) => r.name.endsWith('versions.json'))`, 'versions.json', 5000)
        await sleep(300)
        const settled = await headingY()
        check(Math.abs(served - settled) < 1, `${tag}: ${path} does not move after it loads (heading at ${served} as served, ${settled} once loaded)`)
      }
      const inMenu = (text) => `[...document.querySelectorAll('[data-slot=dropdown-menu-radio-item]')].find((e) => e.textContent.includes(${JSON.stringify(text)}))`
      const switcher = `[...document.querySelectorAll('button[aria-label="Documentation version"]')].find((b) => b.getClientRects().length)`
      await open(at.gettingStarted)
      check(await ev(`document.querySelector('article [role=alert]')?.textContent.includes('Unreleased (main)')`), `${tag}: next shows the "Unreleased (main)" banner`)
      await mouse(switcher)
      check(await until(`!!${inMenu('v')}`, 'the version menu'), `${tag}: the version switcher opens`)
      check(await ev(`!!${inMenu('next (main)')} && !!${inMenu(`${latest.name} (latest)`)}`), `${tag}: the switcher lists next (main) and ${latest.name} (latest)`)
      await shot(`${tag}-version-switcher`)
      await mouse(inMenu(latest.name))
      await until(`location.pathname === '${root}${latest.path}${at.gettingStarted}'`, 'the same page in the release')
      check(await ev(`location.pathname`) === `${root}${latest.path}${at.gettingStarted}`, `${tag}: switching keeps the page path when it exists in ${latest.name}`)
      await ready()
      check(!(await ev(`!!document.querySelector('article [role=alert]')`)), `${tag}: the newest release has no banner`)
      await open(at.architecture)
      const there = (await fetch(origin + root + latest.path + at.architecture, { method: 'HEAD' })).ok
      await mouse(switcher)
      await until(`!!${inMenu('v')}`, 'the version menu')
      await mouse(inMenu(latest.name))
      await until(`location.pathname.startsWith('${root}${latest.path}')`, 'the release')
      check(await ev(`location.pathname`) === `${root}${latest.path}${there ? at.architecture : ''}`, `${tag}: switching to a page ${latest.name} lacks goes to its overview (page there: ${there})`)
      await ready()
      await search('adopt')
      await key('Enter')
      await until(`!location.pathname.endsWith('${root}${latest.path}')`, 'a search result')
      check((await ev(`location.pathname`)).startsWith(`${root}${latest.path}`), `${tag}: search in ${latest.name} stays in that version`)
      await ready()
      fakeNewer.on = true // a release newer than this build exists: the old-version banner
      await go(`${origin}${root}${latest.path}${at.gettingStarted}`)
      await ready()
      await until(`!!document.querySelector('article [role=alert]')`, 'the old-version banner')
      const banner = await ev(`document.querySelector('article [role=alert]')?.textContent`)
      check(banner?.includes(`You are viewing the docs for ${latest.name}`) && banner.includes('The latest is v9.9.9'), `${tag}: an old version warns "${banner}"`)
      await shot(`${tag}-old-version-banner`)
      await open(at.gettingStarted)
      await until(`document.querySelector('article [role=alert]')?.textContent.includes('v9.9.9')`, 'the latest release in the next banner')
      check(await ev(`document.querySelector('article [role=alert]')?.textContent.includes('The latest release is v9.9.9')`), `${tag}: next's banner names the latest release`)
      fakeNewer.on = false

      if (!mobile) {
        // The sidebar hides, the choice is remembered, the content widens.
        const w = () => ev(`document.querySelector('article').getBoundingClientRect().width`)
        const before = await w()
        await click('[data-slot=sidebar-trigger]')
        await until(`document.querySelector('article').getBoundingClientRect().width > ${before}`, 'the content to widen') // the sidebar slides out
        check((await w()) > before, `${tag}: content widens when the sidebar is hidden`)
        await shot(`${tag}-sidebar-hidden`)
        await open(at.rotateKeys)
        await until(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'collapsed'`, 'the remembered sidebar state')
        check(await ev(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'collapsed'`), `${tag}: hidden sidebar is remembered`)
        await press('b', 2, `document.querySelector('[data-slot=sidebar]')?.dataset.state === 'expanded'`, 'Ctrl-B')
        check(await ev(`document.querySelector('[data-slot=sidebar]')?.dataset.state === 'expanded'`), `${tag}: Ctrl-B shows the sidebar again`)
        // Code blocks get a copy button and GitHub alerts render as the preactcn Alert.
        await open(at.contributing)
        await until(`!!document.querySelector('article pre + div [aria-label="Copy code"]')`, 'the copy button')
        check(await ev(`!!document.querySelector('article pre + div [aria-label="Copy code"]')`), `${tag}: code blocks have a copy button`)
        check(await ev(`!!document.querySelector('article [data-slot=alert]')`), `${tag}: a GitHub alert renders as an Alert`)
        await shot(`${tag}-contributing`)
        for (const name of ['home', 'concept', 'cli', 'crd']) {
          await open(at[name])
          await shot(`${tag}-${name}`)
        }
      } else {
        // Phone: the sidebar is a drawer, "On this page" a collapsible above the article.
        await open(at.architecture)
        await click('[data-slot=sidebar-trigger]')
        check(await until(`!!document.querySelector('[data-mobile=true]')`, 'the drawer'), `${tag}: the menu button opens the drawer`)
        await shot(`${tag}-drawer`)
        await key('Escape')
        await until(`!document.querySelector('[data-mobile=true]')`, 'the drawer to close')
        await disclosure(tag, 'On this page', `[...document.querySelectorAll('button')].find((b) => b.textContent.includes('On this page'))`)
        await ev(`[...document.querySelectorAll('button')].find((b) => b.textContent.includes('On this page'))?.click()`)
        await shot(`${tag}-toc`)
        check(await ev(`document.documentElement.scrollWidth <= window.innerWidth`), `${tag}: architecture page has no sideways scroll`)
      }

      // Diagrams (docs/images/*.drawio.svg): the page names the light image, the site adds the dark one and shows the
      // one for the theme. Both must load, only one of each pair may be visible, and the page must not scroll sideways.
      for (const route of diagrams) {
        const name = route.replace(/\/$/, '').replaceAll('/', '-') || 'home'
        await open(route)
        const imgs = `[...document.querySelectorAll('article img[src*=".drawio.svg"]')]`
        const shown = `${imgs}.filter((i) => i.getClientRects().length)`
        await until(`${imgs}.length >= 2 && ${imgs}.length % 2 === 0 && ${imgs}.every((i) => i.complete)`, `the ${name} images`)
        check(await ev(`${imgs}.length >= 2 && ${imgs}.length % 2 === 0 && ${shown}.length === ${imgs}.length / 2`), `${tag}: ${name} has a light and a dark image of each diagram, one visible`)
        check(await ev(`${shown}.every((i) => i.src.endsWith('.${theme}.drawio.svg') && i.naturalWidth > 0)`), `${tag}: ${name} shows its ${theme} images, loaded`)
        check(await ev(`${shown}.every((i) => i.alt.length > 40 && !!i.closest('p')?.nextElementSibling?.textContent.trim())`), `${tag}: ${name} has alt text and a caption for each diagram`)
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
