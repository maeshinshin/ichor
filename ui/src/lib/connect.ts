import { Code, ConnectError, createClient, type Interceptor } from '@connectrpc/connect'
import { clearToken, storedRefresh, storedToken } from '@/lib/oidc'
import { ensureFresh, refreshSession } from '@/lib/session'
import { createConnectTransport } from '@connectrpc/connect-web'
import { ClusterService } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineService } from '@/gen/ichor/v1alpha1/machine_pb'
import { NodeService } from '@/gen/ichor/v1alpha1/node_pb'
import { PatchService } from '@/gen/ichor/v1alpha1/patch_pb'
import { OperationService } from '@/gen/ichor/v1alpha1/operation_pb'
import { DiagnosticsService } from '@/gen/ichor/v1alpha1/diagnostics_pb'
import { UserService } from '@/gen/ichor/v1alpha1/user_pb'
import { CniService } from '@/gen/ichor/v1alpha1/cni_pb'
import { ReclaimService } from '@/gen/ichor/v1alpha1/reclaim_pb'
import { SettingsService } from '@/gen/ichor/v1alpha1/settings_pb'

// "the browser talks only to the backend" (architecture.md, "CLI and UI"):
// the transport's baseUrl is this page's own origin, never a third-party
// host. cmd/ichor-ui registers the three generated handlers on the same
// mux that serves this SPA, so same-origin is also same-server.
//
// With OIDC on (docs/adr/0058) every call carries the signed-in user's ID token;
// a rejected token is refreshed with the refresh token of the ichor sign-in (docs/adr/0094, 2026-11-04)
// and a unary call is retried once; when the session is over (or there is no refresh token, as with an
// external IdP) the token is dropped and the page reloads, which starts a new login.
// With OIDC off there is no token and no header.
const withToken: Interceptor = (next) => async (req) => {
  await ensureFresh()
  const token = storedToken()
  if (token) req.header.set('Authorization', `Bearer ${token}`)
  try {
    return await next(req)
  } catch (e) {
    if (!token || ConnectError.from(e).code !== Code.Unauthenticated) throw e
    const refreshed = storedRefresh() ? await refreshSession() : 'ended'
    if (refreshed === 'ok' && !req.stream) {
      req.header.set('Authorization', `Bearer ${storedToken()}`)
      return await next(req)
    }
    if (refreshed === 'ended') {
      clearToken()
      window.location.reload()
    }
    throw e
  }
}

const transport = createConnectTransport({ baseUrl: window.location.origin, interceptors: [withToken] })

export const clusterClient = createClient(ClusterService, transport)
export const machineClient = createClient(MachineService, transport)
export const nodeClient = createClient(NodeService, transport)
export const patchClient = createClient(PatchService, transport)
export const operationClient = createClient(OperationService, transport)
export const diagnosticsClient = createClient(DiagnosticsService, transport)
export const userClient = createClient(UserService, transport)
export const cniClient = createClient(CniService, transport)
export const reclaimClient = createClient(ReclaimService, transport)
export const settingsClient = createClient(SettingsService, transport)
