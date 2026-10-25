import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { ClusterService } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineService } from '@/gen/ichor/v1alpha1/machine_pb'
import { NodeService } from '@/gen/ichor/v1alpha1/node_pb'

// "the browser talks only to the backend" (architecture.md, "CLI and UI"):
// the transport's baseUrl is this page's own origin, never a third-party
// host. cmd/ichor-ui registers the three generated handlers on the same
// mux that serves this SPA, so same-origin is also same-server.
const transport = createConnectTransport({ baseUrl: window.location.origin })

export const clusterClient = createClient(ClusterService, transport)
export const machineClient = createClient(MachineService, transport)
export const nodeClient = createClient(NodeService, transport)
