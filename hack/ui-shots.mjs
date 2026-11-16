// Screenshots of the UI at desktop and phone widths, light and dark, with
// headless Chromium (from the dev shell) over the DevTools protocol. No npm
// dependency: Node's own WebSocket and fetch are enough.
//
//   make ui-shots OUT=.ui-shots/x [PATHS="/config /nodes"]
//       starts its own Vite (ui/) against a stand-in backend (hack/ui-shots-backend.mjs)
//       and signs the tab in with an unsigned token, so pages behind sign-in render.
//   make ui-shots OUT=.ui-shots/x URL=http://host/ TOKEN=<ID token> [PATHS=...]
//       shoots a running server. TOKEN is an ID token that server issued: copy the
//       ichor.idToken value from a browser signed in to it (DevTools, Session Storage).
//   node hack/ui-shots.mjs <outDir> <baseURL or ""> [path ...]
//
// Signing in: the UI keeps its ID token in sessionStorage under ichor.idToken, which is per tab, so the
// harness seeds it in its own tab, on /login before the first page, and never on a later document: a
// re-seed after the UI drops a refused token would hide the refusal. The UI reads the token's claims
// without checking the signature, and tokenUsable checks only exp (ui/src/lib/oidc.ts), which is why an
// unsigned token gets past the UI. The real ichor-ui does check the signature (internal/uiapi/auth.go),
// so the unsigned token works only against the stand-in backend.
//
// A page that is not the signed-in UI fails the run: the sign-in form, a dropped token, no app shell, or
// (stand-in only) a missing cluster name. No screenshot is written for it.
//
// CLICK="Button text" clicks that button first (to show a dialog); never use it on a button that writes.
// SCROLLTO=id scrolls that element to the top first (use with FULL unset).
// FULL=1 captures the whole page height (up to 4000px), not just the viewport.
// WAIT=ms is a cap (default 20000), not a fixed delay: after each navigate, CLICK and SCROLLTO the
// script polls until the page is loaded and shows no "Loading" text, skeleton or aria-busy element,
// then waits 300 ms for paint. If the cap is hit it says so on stderr and shoots anyway.
// Read-only: it only navigates (GET).
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { createServer as netServer } from 'node:net'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as sleep } from 'node:timers/promises'
import { ANSWERED, CLUSTER, assertGenerated, startBackend, unsignedToken } from './ui-shots-backend.mjs'

const UI = fileURLToPath(new URL('../ui/', import.meta.url))
const DEFAULT_PATHS = ['/', '/clusters', '/machines', '/patches', '/operations']
const sizes = [
  ['desktop', 1440, 900, false],
  ['phone', 390, 844, true],
]
const themes = ['light', 'dark']
const cap = Number(process.env.WAIT ?? 20000)
// True while the page is still loading: document not complete, "Loading…" text (common.loading and the per-page keys), a skeleton or an aria-busy element.
const LOADING = `document.readyState !== 'complete' || document.body.innerText.includes('Loading') || document.querySelector('[data-slot="skeleton"], [aria-busy="true"]') !== null`
// What the page is, read after it settles. The app shell (AppShell's SidebarProvider, data-slot="sidebar-wrapper")
// renders only once signed in, at every width (the sidebar itself is a sheet on a phone).
const AUTH_STATE = (expectText) => `({
  token: !!sessionStorage.getItem('ichor.idToken'),
  shell: !!document.querySelector('[data-slot="sidebar-wrapper"]'),
  signIn: !!document.querySelector('input[type="password"]'),
  // textContent, not innerText: the cluster name sits in a badge hidden on a phone, and innerText leaves hidden text out.
  hasText: ${JSON.stringify(expectText)} === null || document.body.textContent.includes(${JSON.stringify(expectText)}),
  snippet: (document.body.innerText || '').replace(/\\s+/g, ' ').slice(0, 200),
})`

// authProblem names why the page is not the signed-in UI, or returns '' when it is.
function authProblem(st, expectText, hint) {
  if (st.signIn) return `the sign-in form is showing (${hint})`
  if (!st.token) return `the ID token is gone from sessionStorage: the UI dropped it because the backend refused it (${hint})`
  if (!st.shell) return `the signed-in shell did not render; the page says: "${st.snippet}"`
  if (!st.hasText) return `the backend's data (${expectText}) is not on the page`
  return ''
}

// freePort asks the kernel for an unused loopback port.
async function freePort() {
  const srv = netServer()
  await new Promise((r) => srv.listen(0, '127.0.0.1', r))
  const { port } = srv.address()
  await new Promise((r) => srv.close(r))
  return port
}

// startVite runs the UI's dev server against the stand-in backend and resolves to its origin.
async function startVite(backendURL, cleanup) {
  const port = await freePort()
  const vite = spawn(join(UI, 'node_modules/.bin/vite'), ['--port', String(port), '--strictPort', '--host', '127.0.0.1'], {
    cwd: UI,
    env: { ...process.env, ICHOR_API: backendURL },
    stdio: ['ignore', 'ignore', 'inherit'],
  })
  cleanup.push(() => vite.kill())
  const origin = `http://127.0.0.1:${port}`
  for (let i = 0; i < 300; i++) {
    try {
      if ((await fetch(new URL('/login', origin))).ok) return origin
    } catch {
      // not listening yet
    }
    await sleep(200)
  }
  throw new Error(`vite did not start on ${origin}`)
}

async function main() {
  const [outDir, base, ...rest] = process.argv.slice(2)
  if (!outDir || base === undefined) {
    console.error('usage: node hack/ui-shots.mjs <outDir> <baseURL or ""> [path ...]')
    process.exit(2)
  }
  const paths = rest.length ? rest : DEFAULT_PATHS
  const cleanup = []
  try {
    let origin = base
    let token = process.env.TOKEN
    let expectText = null
    let hint = 'set TOKEN to an ID token from a browser signed in to this server'
    if (!origin) {
      if (!existsSync(join(UI, 'node_modules/.bin/vite'))) throw new Error('ui/node_modules is missing; run make ui-install')
      assertGenerated(ANSWERED)
      const backend = await startBackend()
      cleanup.push(() => backend.close())
      origin = await startVite(backend.url, cleanup)
      token ??= unsignedToken()
      expectText = CLUSTER
      hint = 'the seed did not take, or the stand-in backend refused the token'
    }
    mkdirSync(outDir, { recursive: true })

    const port = 9300 + Math.floor(Math.random() * 500)
    const chrome = spawn(
      process.env.CHROMIUM ?? 'chromium',
      ['--headless=new', '--no-sandbox', '--disable-gpu', `--remote-debugging-port=${port}`, '--user-data-dir=' + join(outDir, '.profile'), 'about:blank'],
      { stdio: 'ignore' },
    )
    cleanup.push(() => chrome.kill())
    let version
    for (let i = 0; i < 100 && !version; i++) {
      try {
        version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
      } catch {
        await sleep(100)
      }
    }
    const ws = new WebSocket(version.webSocketDebuggerUrl)
    cleanup.push(() => ws.close())
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
    await s('Runtime.enable')
    // settle polls until nothing is loading (LOADING), capped at WAIT ms, then waits for paint.
    // With url, it first waits for the app to have mounted (#app has content and a <main> exists);
    // an idle blank page would otherwise look "not loading".
    const settle = async (label, url) => {
      const deadline = Date.now() + cap
      if (url) {
        const rendered = `!!document.querySelector('#app > *') && !!document.querySelector('main')`
        while (!(await s('Runtime.evaluate', { expression: rendered, returnByValue: true })).result?.value) {
          if (Date.now() >= deadline) {
            console.error(`ui-shots: ${label} app did not render within ${cap} ms (WAIT cap); shooting anyway`)
            break
          }
          await sleep(200)
        }
      }
      while ((await s('Runtime.evaluate', { expression: LOADING, returnByValue: true })).result?.value) {
        if (Date.now() >= deadline) {
          console.error(`ui-shots: ${label} still loading after ${cap} ms (WAIT cap); shooting anyway`)
          break
        }
        await sleep(200)
      }
      await sleep(300)
    }

    // Seed the token once, in this tab, on /login (which needs no sign-in).
    if (token) {
      await s('Page.navigate', { url: new URL('/login', origin).href })
      const loaded = Date.now() + cap
      while ((await s('Runtime.evaluate', { expression: 'document.readyState', returnByValue: true })).result?.value !== 'complete' && Date.now() < loaded) await sleep(100)
      await s('Runtime.evaluate', { expression: `sessionStorage.setItem('ichor.idToken', ${JSON.stringify(token)})`, returnByValue: true })
      const readBack = (await s('Runtime.evaluate', { expression: `sessionStorage.getItem('ichor.idToken')`, returnByValue: true })).result?.value
      if (readBack !== token) throw new Error('could not seed the ID token into sessionStorage')
    }

    for (const p of paths) {
      const url = new URL(p, origin).href
      const needsSignIn = new URL(p, origin).pathname !== '/login'
      for (const [sizeName, width, height, mobile] of sizes) {
        for (const theme of themes) {
          await s('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile })
          await s('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] })
          const { identifier } = await s('Page.addScriptToEvaluateOnNewDocument', {
            source: `try{localStorage.setItem('ichor-ui-theme','${theme}')}catch(e){}`,
          })
          await s('Page.navigate', { url })
          await settle(`${p} ${sizeName} ${theme}`, url)
          if (process.env.CLICK) {
            // CLICK="Shut down cluster" opens a dialog before the shot; it clicks the first button with that exact text.
            const expr = `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${JSON.stringify(process.env.CLICK)}); if (b) b.click(); return !!b })()`
            // The button may render after the data does: poll for it within the cap.
            const deadline = Date.now() + cap
            let clicked = false
            while (!(clicked = (await s('Runtime.evaluate', { expression: expr, returnByValue: true })).result?.value) && Date.now() < deadline) {
              await sleep(200)
            }
            if (!clicked) console.error(`ui-shots: ${p} ${sizeName} ${theme}: button "${process.env.CLICK}" never appeared within ${cap} ms (WAIT cap)`)
            await settle(`${p} ${sizeName} ${theme} after CLICK`)
          }
          if (process.env.SCROLLTO) {
            await s('Runtime.evaluate', { expression: `document.getElementById(${JSON.stringify(process.env.SCROLLTO)})?.scrollIntoView()` })
            await settle(`${p} ${sizeName} ${theme} after SCROLLTO`)
          }
          if (needsSignIn) {
            const st = (await s('Runtime.evaluate', { expression: AUTH_STATE(expectText), returnByValue: true })).result?.value ?? {}
            const problem = authProblem(st, expectText, hint)
            if (problem) throw new Error(`FAILED ${p} ${sizeName} ${theme}: ${problem}. No screenshot was written.`)
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
  } finally {
    for (const stop of cleanup.reverse()) {
      try {
        stop()
      } catch {
        // already stopped
      }
    }
  }
}

main().catch((e) => {
  console.error(`ui-shots: ${e.message}`)
  process.exitCode = 1
})
