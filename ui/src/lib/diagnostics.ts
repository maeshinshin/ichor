// Logic behind the node page's Actions menu: which downloads to offer, the packet
// capture form, and reading a download stream into memory with a bound. Pure: no DOM, no preact, so
// it runs under `node --test`. The click that saves the file is in download.ts.
import type { TalosLink } from '@/gen/ichor/v1alpha1/talosdetails_pb'

export type DownloadKind = 'support-bundle' | 'etcd-snapshot' | 'packet-capture'

// What the caller may download (DiagnosticsService.GetMyPermissions): the machine's three downloads and
// the cluster's CA rotation credentials.
export type Permissions = { supportBundle: boolean; etcdSnapshot: boolean; packetCapture: boolean; clusterCredentials: boolean; clusterUpdate: boolean; talosconfigReader: boolean }

// toPermissions copies the response's flags; a missing response (not answered yet, or the probe failed)
// stays undefined, which offers nothing.
export function toPermissions(res: Permissions | undefined): Permissions | undefined {
  return res && {
    supportBundle: res.supportBundle,
    etcdSnapshot: res.etcdSnapshot,
    packetCapture: res.packetCapture,
    clusterCredentials: res.clusterCredentials,
    clusterUpdate: res.clusterUpdate,
    talosconfigReader: res.talosconfigReader,
  }
}

// The server enforces these too; the form says so before the call.
export const maxCaptureSeconds = 300
export const defaultCaptureSeconds = 30
// The browser keeps the whole file in memory until it is saved, so a very large download is refused
// here rather than taking the tab down. The CLI has no such limit.
export const maxBrowserBytes = 1024 * 1024 * 1024

// availableActions lists the downloads to show: only those the user may start, and an etcd snapshot
// only on a control plane node.
export function availableActions(perms: Permissions | undefined, controlPlane: boolean): DownloadKind[] {
  if (!perms) return []
  const out: DownloadKind[] = []
  if (perms.supportBundle) out.push('support-bundle')
  if (perms.etcdSnapshot && controlPlane) out.push('etcd-snapshot')
  if (perms.packetCapture) out.push('packet-capture')
  return out
}

// captureInterfaces lists the node's links for the capture form and picks the first choice: eth0 if
// it exists, else the first physical link that is up, else the first link.
export function captureInterfaces(links: Pick<TalosLink, 'name' | 'linkUp' | 'kind'>[]): { names: string[]; initial: string } {
  const names = links.map((l) => l.name).filter((n) => n !== '')
  const initial =
    names.find((n) => n === 'eth0') ?? links.find((l) => l.linkUp && l.kind === '' && l.name !== '')?.name ?? names[0] ?? ''
  return { names, initial }
}

// parseDuration reads "30", "30s" or "2m" as whole seconds, between 1 and maxCaptureSeconds.
export function parseDuration(text: string): number | undefined {
  const m = /^\s*(\d{1,4})\s*([sm]?)\s*$/.exec(text)
  if (!m) return undefined
  const seconds = Number(m[1]) * (m[2] === 'm' ? 60 : 1)
  return seconds >= 1 && seconds <= maxCaptureSeconds ? seconds : undefined
}

const filterLine = /^\{\s*0x[0-9a-fA-F]{1,4},\s*\d{1,3},\s*\d{1,3},\s*0x[0-9a-fA-F]{1,8}\s*\},?$/

// filterProblem returns what is wrong with a `tcpdump -dd` filter, or undefined. The server checks the
// program properly (jumps, the ending return); this catches pasted expressions such as "tcp port 80".
export function filterProblem(text: string): string | undefined {
  const lines = text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '')
  const bad = lines.findIndex((l) => !filterLine.test(l))
  if (bad >= 0) return `line ${bad + 1} is not in tcpdump -dd format, for example { 0x28, 0, 0, 0x0000000c },`
  if (lines.length > 4096) return 'a filter has at most 4096 instructions'
  return undefined
}

export type CaptureForm = { iface: string; duration: string; filter: string }

export type CaptureCheck =
  | { ok: true; interface: string; durationSeconds: number; bpfFilter: string }
  | { ok: false; errors: { iface?: string; duration?: string; filter?: string } }

export function checkCapture(f: CaptureForm): CaptureCheck {
  const errors: { iface?: string; duration?: string; filter?: string } = {}
  if (!/^[A-Za-z0-9_.:@-]{1,32}$/.test(f.iface)) errors.iface = 'Choose an interface.'
  const seconds = parseDuration(f.duration)
  if (seconds === undefined) errors.duration = `Enter a time from 1 second to ${maxCaptureSeconds / 60} minutes, such as 30s or 2m.`
  const filter = f.filter.trim()
  const problem = filter ? filterProblem(filter) : undefined
  if (problem) errors.filter = problem
  if (Object.keys(errors).length > 0 || seconds === undefined) return { ok: false, errors }
  return { ok: true, interface: f.iface, durationSeconds: seconds, bpfFilter: filter }
}

export type Chunk = { filename: string; contentType: string; data: Uint8Array }

export type Collected = { filename: string; contentType: string; parts: Uint8Array<ArrayBuffer>[]; size: number }

export class DownloadTooLarge extends Error {
  limit: number
  constructor(limit: number) {
    super(`The file is larger than ${Math.round(limit / (1024 * 1024))} MiB, which a browser cannot hold safely.`)
    this.limit = limit
  }
}

// collect reads a download stream to its end, keeping the parts in order. It stops with DownloadTooLarge
// past maxBytes, and rethrows a stream error so a half file is never offered for saving.
export async function collect(stream: AsyncIterable<Chunk>, maxBytes: number = maxBrowserBytes): Promise<Collected> {
  const out: Collected = { filename: '', contentType: 'application/octet-stream', parts: [], size: 0 }
  for await (const c of stream) {
    if (c.filename && !out.filename) out.filename = c.filename
    if (c.contentType && out.parts.length === 0) out.contentType = c.contentType
    out.size += c.data.length
    if (out.size > maxBytes) throw new DownloadTooLarge(maxBytes)
    if (c.data.length > 0) out.parts.push(new Uint8Array(c.data))
  }
  return out
}

// fallbackName is used if the server sent none (it always does).
export function fileNameOf(c: Collected, kind: DownloadKind | 'ca-credentials' | 'reader-talosconfig'): string {
  if (c.filename) return c.filename
  return { 'support-bundle': 'support-bundle.zip', 'etcd-snapshot': 'etcd.snapshot.db', 'packet-capture': 'capture.pcap', 'ca-credentials': 'credentials.zip', 'reader-talosconfig': 'talosconfig-reader.yaml' }[kind]
}
