// The browser side of a download: the bytes arrive as a Connect stream through the same
// authenticated transport as every other call (so the bearer token travels in a header, not a cookie, and
// a cross-site request cannot start one), and are saved through a temporary object URL. Kept out of
// diagnostics.ts because it needs the DOM.
import { diagnosticsClient, operationClient, userClient } from '@/lib/connect'
import { downloadName } from '@/lib/users'
import { collect, fileNameOf } from '@/lib/diagnostics'
import type { CapturePacketsRequest } from '@/gen/ichor/v1alpha1/diagnostics_pb'

type Ref = { namespace: string; name: string }

export type DiagnosticsDownloadRequest =
  | { kind: 'support-bundle'; machine: Ref }
  | { kind: 'etcd-snapshot'; machine: Ref }
  | { kind: 'packet-capture'; capture: Pick<CapturePacketsRequest, 'interface' | 'bpfFilter' | 'durationSeconds'> & { machine: Ref } }

// The new credentials of a CA rotation are saved the same way.
// An os:reader talosconfig for talosctl too.
export type DownloadRequest =
  | DiagnosticsDownloadRequest
  | { kind: 'ca-credentials'; operation: Ref; lifetimeSeconds?: number; readOnly?: boolean }
  | { kind: 'reader-talosconfig'; cluster: Ref; lifetimeSeconds?: number }

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
    case 'reader-talosconfig':
      return diagnosticsClient.downloadReaderTalosconfig({ cluster: req.cluster, lifetimeSeconds: BigInt(req.lifetimeSeconds ?? 0) }, { signal })
  }
}

// saveFile hands the bytes to the browser as a download, through a temporary object URL.
function saveFile(parts: BlobPart[], type: string, name: string): void {
  const url = URL.createObjectURL(new Blob(parts, { type }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.append(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}

// download runs the call to its end, saves the file and returns its name. Nothing is saved if the
// stream breaks or is too large for the browser.
export async function download(req: DownloadRequest, signal: AbortSignal): Promise<string> {
  const got = await collect(open(req, signal))
  const name = fileNameOf(got, req.kind)
  saveFile(got.parts, got.contentType, name)
  return name
}

// The caller's own kubeconfig: public data only, saved as a text file.
export async function downloadKubeconfig(): Promise<string> {
  const res = await userClient.getMyKubeconfig({})
  const name = downloadName(res.filename, 'kubeconfig.yaml')
  saveFile([res.kubeconfig], 'application/yaml', name)
  return name
}
