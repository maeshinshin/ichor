// The browser side of a download (docs/adr/0084): the bytes arrive as a Connect stream through the same
// authenticated transport as every other call (so the bearer token travels in a header, not a cookie, and
// a cross-site request cannot start one), and are saved through a temporary object URL. Kept out of
// diagnostics.ts because it needs the DOM.
import { diagnosticsClient } from '@/lib/connect'
import { collect, fileNameOf, type Chunk, type DownloadKind } from '@/lib/diagnostics'
import type { CapturePacketsRequest } from '@/gen/ichor/v1alpha1/diagnostics_pb'

type Ref = { namespace: string; name: string }

async function* chunksOf(stream: AsyncIterable<{ filename: string; contentType: string; data: Uint8Array }>): AsyncGenerator<Chunk> {
  for await (const c of stream) yield { filename: c.filename, contentType: c.contentType, data: c.data }
}

export type DownloadRequest =
  | { kind: 'support-bundle'; machine: Ref }
  | { kind: 'etcd-snapshot'; machine: Ref }
  | { kind: 'packet-capture'; capture: Pick<CapturePacketsRequest, 'interface' | 'bpfFilter' | 'durationSeconds'> & { machine: Ref } }

function open(req: DownloadRequest, signal: AbortSignal): AsyncIterable<{ filename: string; contentType: string; data: Uint8Array }> {
  switch (req.kind) {
    case 'support-bundle':
      return diagnosticsClient.downloadSupportBundle({ target: { case: 'machine', value: req.machine } }, { signal })
    case 'etcd-snapshot':
      return diagnosticsClient.downloadEtcdSnapshot({ machine: req.machine }, { signal })
    case 'packet-capture':
      return diagnosticsClient.capturePackets(req.capture, { signal })
  }
}

// download runs the call to its end, saves the file and returns its name. Nothing is saved if the
// stream breaks or is too large for the browser.
export async function download(req: DownloadRequest, signal: AbortSignal): Promise<string> {
  const got = await collect(chunksOf(open(req, signal)))
  const name = fileNameOf(got, req.kind as DownloadKind)
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
