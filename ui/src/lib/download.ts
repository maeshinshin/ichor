// The browser side of a download: the bytes arrive as a Connect stream through the same
// authenticated transport as every other call (so the bearer token travels in a header, not a cookie, and
// a cross-site request cannot start one), and are saved through a temporary object URL. Kept out of
// diagnostics.ts because it needs the DOM.
import { diagnosticsClient, operationClient, userClient } from '@/lib/connect'
import { downloadName } from '@/lib/users'
import { collect, etcdSnapshotForm, etcdSnapshotPath, fileNameOf } from '@/lib/diagnostics'
import { storedToken } from '@/lib/oidc'
import { refusalWithin } from '@/lib/snapshot'
import type { CapturePacketsRequest } from '@/gen/ichor/v1alpha1/diagnostics_pb'

type Ref = { namespace: string; name: string }

export type DiagnosticsDownloadRequest =
  | { kind: 'support-bundle'; machine: Ref }
  | { kind: 'etcd-snapshot'; machine: Ref } // saved by saveEtcdSnapshot, not download
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
      throw new Error('an etcd snapshot is saved by saveEtcdSnapshot')
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
// stream breaks.
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

// saveEtcdSnapshot hands the snapshot to the browser as a form post to a hidden frame, so the browser writes
// the response to disk as it arrives and neither the page nor ichor-ui holds the file, whatever its size.
// The frame stays empty while a download runs; if the server refuses, it shows the reason, which is thrown.
// `late` gets a refusal that arrives after the wait, which the server can send after 20 s without credentials.
export async function saveEtcdSnapshot(machine: Ref, late: (reason: string) => void): Promise<void> {
  // A cheap call first: it refreshes an expired ID token, which a form post cannot do.
  await userClient.whoAmI({})
  const frame = document.createElement('iframe')
  frame.name = `snapshot-${crypto.randomUUID()}`
  frame.hidden = true
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = etcdSnapshotPath
  form.target = frame.name
  for (const [name, value] of Object.entries(etcdSnapshotForm(storedToken() ?? '', machine))) {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = name
    input.value = value
    form.append(input)
  }
  const refused = new Promise<string>((resolve) => {
    // A download fires no load event; a refusal is a page, and its text is the reason.
    frame.addEventListener('load', () => resolve(frame.contentDocument?.body?.innerText.trim() ?? ''))
  })
  document.body.append(frame, form)
  form.submit()
  const reason = await refusalWithin(refused, 15_000, (r) => {
    frame.remove()
    late(r)
  })
  form.remove()
  // The frame stays: removing it would cancel a download still running.
  if (reason) {
    frame.remove()
    throw new Error(reason)
  }
}
