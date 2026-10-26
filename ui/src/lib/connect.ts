import { Code, ConnectError, createClient, type Interceptor } from '@connectrpc/connect'
import { clearToken, storedToken } from '@/lib/oidc'
import { createConnectTransport } from '@connectrpc/connect-web'
import { ClusterService } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineService } from '@/gen/ichor/v1alpha1/machine_pb'
import { NodeService } from '@/gen/ichor/v1alpha1/node_pb'
import { PatchService } from '@/gen/ichor/v1alpha1/patch_pb'
import { GitOpsService } from '@/gen/ichor/v1alpha1/gitops_pb'
import { OperationService } from '@/gen/ichor/v1alpha1/operation_pb'

// "the browser talks only to the backend" (architecture.md, "CLI and UI"):
// the transport's baseUrl is this page's own origin, never a third-party
// host. cmd/ichor-ui registers the three generated handlers on the same
// mux that serves this SPA, so same-origin is also same-server.
//
// With OIDC on every call carries the signed-in user's ID token;
// a rejected token is dropped and the page reloads, which starts a new login.
// With OIDC off there is no token and no header.
const withToken: Interceptor = (next) => async (req) => {
  const token = storedToken()
  if (token) req.header.set('Authorization', `Bearer ${token}`)
  try {
    return await next(req)
  } catch (e) {
    if (token && ConnectError.from(e).code === Code.Unauthenticated) {
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
export const gitOpsClient = createClient(GitOpsService, transport)
export const operationClient = createClient(OperationService, transport)
