// The browser side of a download (docs/adr/0084): the bytes arrive as a Connect stream through the same
// authenticated transport as every other call (so the bearer token travels in a header, not a cookie, and
// a cross-site request cannot start one), and are saved through a temporary object URL. Kept out of
// diagnostics.ts because it needs the DOM.
import { diagnosticsClient, operationClient, userClient } from '@/lib/connect'
import { downloadName } from '@/lib/users'
import { collect, fileNameOf, type Chunk } from '@/lib/diagnostics'
import type { CapturePacketsRequest } from '@/gen/ichor/v1alpha1/diagnostics_pb'

type Ref = { namespace: string; name: string }

async function* chunksOf(stream: AsyncIterable<{ filename: string; contentType: string; data: Uint8Array }>): AsyncGenerator<Chunk> {
  for await (const c of stream) yield { filename: c.filename, contentType: c.contentType, data: c.data }
}

export type DiagnosticsDownloadRequest =
  | { kind: 'support-bundle'; machine: Ref }
  | { kind: 'etcd-snapshot'; machine: Ref }
  | { kind: 'packet-capture'; capture: Pick<CapturePacketsRequest, 'interface' | 'bpfFilter' | 'durationSeconds'> & { machine: Ref } }

// The new credentials of a CA rotation (docs/adr/0093) are saved the same way.
export type DownloadRequest = DiagnosticsDownloadRequest | { kind: 'ca-credentials'; operation: Ref; lifetimeSeconds?: number; readOnly?: boolean }

function open(req: DownloadRequest, signal: AbortSignal): AsyncIterable<{ filename: string; contentType: string; data: Uint8Array }> {
  switch (req.kind) {
    case 'support-bundle':
      return diagnosticsClient.downloadSupportBundle({ target: { case: 'machine', value: req.machine } }, { signal })
    case 'etcd-snapshot':
      return diagnosticsClient.downloadEtcdSnapshot({ machine: req.machine }, { signal })
    case 'packet-capture':
      return diagnosticsClient.capturePackets(req.capture, { signal })
    case 'ca-credentials':
      return operationClient.downloadCARotationCredentials(
        { operation: req.operation, lifetimeSeconds: BigInt(req.lifetimeSeconds ?? 0), readOnly: req.readOnly ?? false },
        { signal },
      )
  }
}

// download runs the call to its end, saves the file and returns its name. Nothing is saved if the
// stream breaks or is too large for the browser.
export async function download(req: DownloadRequest, signal: AbortSignal): Promise<string> {
  const got = await collect(chunksOf(open(req, signal)))
  const name = fileNameOf(got, req.kind)
  const url = URL.createObjectURL(new Blob(got.parts, { type: got.contentType }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.append(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
  return name
}

// The caller's own kubeconfig (docs/adr/0094): public data only, saved as a text file.
export async function downloadKubeconfig(): Promise<string> {
  const res = await userClient.getMyKubeconfig({})
  const name = downloadName(res.filename, 'kubeconfig.yaml')
  const url = URL.createObjectURL(new Blob([res.kubeconfig], { type: 'application/yaml' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.append(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
  return name
}
