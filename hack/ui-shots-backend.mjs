// A stand-in for ichor-ui's Connect API, for `make ui-shots` only. The page under review calls it, and
// its answers are the fixtures below, so a screenshot from it shows this data, not a cluster's.
//
// It does NOT verify signatures. The real ichor-ui does (internal/uiapi/auth.go, LocalVerifier: signature,
// issuer, audience ichor-ui, expiry). Here a bearer token only has to name a subject and not have ended,
// which is what lets hack/ui-shots.mjs sign in with an unsigned token. Never point a real server at this
// token; a real server needs TOKEN, an ID token it issued.
//
// Drift: assertGenerated refuses to start when a fixture names an RPC the generated client does not have,
// so a renamed RPC stops the run instead of showing an empty page. An RPC without a fixture is answered
// with an empty message and logged once on stderr. A renamed field is not caught here: it shows up as a
// missing value on the page, which the harness checks for CLUSTER.
import { createServer } from 'node:http'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

export const CLUSTER = 'mock-cluster'

const GEN = fileURLToPath(new URL('../ui/src/gen/ichor/v1alpha1/', import.meta.url))

const AUTH_CONFIG = { enabled: true, providers: [{ id: 'ichor', issuer: '', clientId: '' }] }

const FIXTURES = {
  '/ichor.v1alpha1.ClusterService/ListClusters': () => ({
    clusters: [
      {
        ref: { namespace: 'ichor-system', name: CLUSTER },
        talosVersion: 'v1.14.3',
        kubernetesVersion: 'v1.36.2',
        endpoint: '10.0.0.5',
        controlPlaneCount: 3,
        controlPlaneSchedulable: true,
      },
    ],
  }),
  '/ichor.v1alpha1.UserService/WhoAmI': () => ({
    username: 'ichor:ui-shots',
    groups: [],
    source: 'ichor',
    displayName: 'UI shots',
    mustChangePassword: false,
  }),
  '/ichor.v1alpha1.PatchService/ListPatches': () => ({
    patches: [
      {
        ref: { namespace: 'ichor-system', name: 'mock-sysctl' },
        cluster: { namespace: 'ichor-system', name: CLUSTER },
        layer: 'CONFIG_LAYER_CLUSTER',
        priority: 10,
        config: '{"machine":{"sysctls":{"net.core.somaxconn":"1024"}}}',
      },
    ],
  }),
}

// The paths the fixtures answer, checked against the generated client by assertGenerated.
export const ANSWERED = Object.keys(FIXTURES)

// An unsigned ID token (alg none, empty signature) that the UI accepts and the stand-in accepts.
export function unsignedToken(nowMs = Date.now()) {
  const b64 = (o) => Buffer.from(JSON.stringify(o)).toString('base64url')
  const now = Math.floor(nowMs / 1000)
  return `${b64({ alg: 'none', typ: 'JWT' })}.${b64({ iss: 'ichor', aud: 'ichor-ui', sub: 'ui-shots', iat: now, exp: now + 3600 })}.`
}

export function assertGenerated(paths = ANSWERED) {
  const generated = readdirSync(GEN)
    .filter((f) => f.endsWith('_pb.ts'))
    .map((f) => readFileSync(join(GEN, f), 'utf8'))
    .join('\n')
  for (const p of paths) {
    const m = p.match(/^\/ichor\.v1alpha1\.(\w+)\/(\w+)$/)
    if (!m || !new RegExp(`from rpc ichor\\.v1alpha1\\.${m[1]}\\.${m[2]}\\b`).test(generated)) {
      throw new Error(`stand-in backend answers ${p}, which ui/src/gen does not generate; update hack/ui-shots-backend.mjs`)
    }
  }
}

// bearerOk: the token names a subject and has not ended. The signature is not checked (see above).
function bearerOk(header = '') {
  try {
    const claims = JSON.parse(Buffer.from((header.slice(7).split('.')[1] ?? ''), 'base64url'))
    return typeof claims.sub === 'string' && claims.sub !== '' && typeof claims.exp === 'number' && claims.exp * 1000 > Date.now()
  } catch {
    return false
  }
}

// Connect's error shape: a JSON body with a code, and the HTTP status the code maps to.
const json = (res, status, body) => {
  res.writeHead(status, { 'Content-Type': 'application/json' })
  res.end(JSON.stringify(body))
}

// startBackend listens on a free loopback port and resolves to its URL and a close function.
export function startBackend() {
  const noFixture = new Set()
  const server = createServer((req, res) => {
    req.resume()
    req.on('end', () => {
      const path = req.url.split('?')[0]
      if (path === '/auth/config') return json(res, 200, AUTH_CONFIG)
      if (!path.startsWith('/ichor.v1alpha1.')) return json(res, 404, { code: 'not_found', message: path })
      if (!bearerOk(req.headers.authorization)) return json(res, 401, { code: 'unauthenticated', message: 'no valid bearer token' })
      const fixture = FIXTURES[path]
      if (!fixture && !noFixture.has(path)) {
        noFixture.add(path)
        console.error(`ui-shots: stand-in backend has no fixture for ${path}; answering an empty message`)
      }
      json(res, 200, fixture ? fixture() : {})
    })
  })
  return new Promise((resolve) =>
    server.listen(0, '127.0.0.1', () =>
      resolve({
        url: `http://127.0.0.1:${server.address().port}`,
        close: () => (server.closeAllConnections(), server.close()),
      }),
    ),
  )
}
