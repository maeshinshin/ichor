import { useEffect, useRef, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { EffectiveConfigValue } from '@/gen/ichor/v1alpha1/node_pb'
import { ConfigLayer, DiskRole, type NodeDetail as NodeDetailMessage, type StreamNodeLogsResponse } from '@/gen/ichor/v1alpha1/node_pb'
import { nodeClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { formatBytes, formatTimestamp } from '@/lib/format'
import { streamWithResume } from '@/lib/resumableStream'

type ErrorShape = { title: string; detail: string }

// Node detail: hardware, disks, network, live resource usage, Kubernetes
// state, effective config (origin-annotated, secrets masked) and logs
// (architecture.md, "CLI and UI").
export function NodeDetail({ namespace, name }: { namespace: string; name: string }) {
  const [node, setNode] = useState<NodeDetailMessage | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => {
    let cancelled = false
    setNode(null)
    setError(null)
    nodeClient.getNode({ machine: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setNode(res.node ?? null)
      },
      (err) => {
        if (!cancelled) setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [namespace, name])

  if (error) return <ErrorAlert {...error} />
  if (!node) return <p class="mt-10 text-center text-sm text-muted-foreground">Loading node…</p>

  return (
    <div class="mt-6 space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>{name}</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <Field label="Manufacturer" value={node.hardware?.manufacturer} />
          <Field label="Model" value={node.hardware?.model} />
          <Field label="Serial number" value={node.hardware?.serialNumber} />
          <Field label="CPU cores" value={node.hardware ? String(node.hardware.cpuCores) : undefined} />
          <Field label="Memory" value={node.hardware ? formatBytes(node.hardware.memoryTotalBytes) : undefined} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Disks</CardTitle>
        </CardHeader>
        <CardContent>
          {node.disks.length === 0 ? (
            <p class="text-sm text-muted-foreground">No disks reported.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Identification</TableHead>
                  <TableHead>Size</TableHead>
                  <TableHead>Role</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {node.disks.map((d, i) => (
                  <TableRow key={i}>
                    <TableCell>{diskIdentification(d)}</TableCell>
                    <TableCell>{formatBytes(d.sizeBytes)}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{enumLabel(DiskRole, d.role)}</Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Network</CardTitle>
        </CardHeader>
        <CardContent>
          {node.networkInterfaces.length === 0 ? (
            <p class="text-sm text-muted-foreground">No network interfaces reported.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Interface</TableHead>
                  <TableHead>MAC</TableHead>
                  <TableHead>Up</TableHead>
                  <TableHead>Addresses</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {node.networkInterfaces.map((n) => (
                  <TableRow key={n.name}>
                    <TableCell>{n.alias || n.name}</TableCell>
                    <TableCell>{n.permanentMac || '-'}</TableCell>
                    <TableCell>
                      <Badge variant={n.up ? 'secondary' : 'outline'}>{n.up ? 'up' : 'down'}</Badge>
                    </TableCell>
                    <TableCell>{n.addresses.length ? n.addresses.join(', ') : '-'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Resource usage</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          {node.resourceUsage ? (
            <>
              <Field label="CPU cores used" value={node.resourceUsage.cpuCoresUsed.toFixed(2)} />
              <Field
                label="Memory"
                value={`${formatBytes(node.resourceUsage.memoryUsedBytes)} / ${formatBytes(node.resourceUsage.memoryTotalBytes)}`}
              />
              <Field
                label="Filesystem"
                value={`${formatBytes(node.resourceUsage.filesystemUsedBytes)} / ${formatBytes(node.resourceUsage.filesystemTotalBytes)}`}
              />
            </>
          ) : (
            <p class="text-sm text-muted-foreground">No resource usage reported.</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Kubernetes</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          {node.kubernetes ? (
            <>
              <Field label="Node name" value={node.kubernetes.nodeName} />
              <Field label="Ready" value={node.kubernetes.ready ? 'yes' : 'no'} />
              <Field label="Kubelet version" value={node.kubernetes.kubeletVersion} />
              <Field label="Schedulable" value={node.kubernetes.schedulable ? 'yes' : 'no'} />
            </>
          ) : (
            <p class="text-sm text-muted-foreground">Not joined to Kubernetes yet.</p>
          )}
        </CardContent>
      </Card>

      <EffectiveConfigSection namespace={namespace} name={name} />
      <LogsSection namespace={namespace} name={name} />
    </div>
  )
}

function diskIdentification(d: { wwid: string; serial: string; byIdPath: string; busPath: string }): string {
  // architecture.md's own fallback chain: WWID, then serial, then by-id
  // path; any of the first three may be empty (node.proto's own comment).
  return d.wwid || d.serial || d.byIdPath || d.busPath || 'nothing stable to key on'
}

function EffectiveConfigSection({ namespace, name }: { namespace: string; name: string }) {
  const [values, setValues] = useState<EffectiveConfigValue[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => {
    let cancelled = false
    setValues(null)
    setError(null)
    nodeClient.getEffectiveConfig({ machine: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setValues(res.config?.values ?? [])
      },
      (err) => {
        if (!cancelled) setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [namespace, name])

  // Grouped bottom-up (architecture.md, "Patch layers": "Patches apply
  // bottom-up: 1. System ... 2. Cluster, 3. role, 4. selector, 5.
  // machine"), which is also ConfigLayer's own numeric order - "The
  // effective-config view in the UI shows which layer each value came
  // from" is this section's one explicit design requirement.
  const groups = groupByLayer(values ?? [])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Effective config</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p class="text-sm text-muted-foreground">
          Read-only: this is what the controller last rendered for this node, grouped by the
          layer each value came from. Editing a patch isn't available from this screen yet.
        </p>
        {error && <ErrorAlert {...error} />}
        {!error && !values && <p class="text-sm text-muted-foreground">Loading…</p>}
        {values && values.length === 0 && <p class="text-sm text-muted-foreground">No values reported.</p>}
        {groups.map(([layer, group]) => (
          <div key={layer}>
            <h4 class="mb-1 text-sm font-medium">{enumLabel(ConfigLayer, layer)}</h4>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Path</TableHead>
                  <TableHead>Value</TableHead>
                  <TableHead>Source</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {group.map((v) => (
                  <TableRow key={v.path}>
                    <TableCell className="font-mono text-xs">{v.path}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {v.value}
                      {/* The server masks any secret value before it reaches the browser
                          (EffectiveConfigValue.secret) - there is nothing here
                          that can or should reveal it. */}
                      {v.secret && (
                        <Badge variant="outline" className="ml-2">
                          secret
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{v.origin?.source || '-'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

// groupByLayer buckets values by origin.layer (ConfigLayer.UNSPECIFIED for
// a value with no origin at all) and returns the groups in ConfigLayer's
// own numeric order, which is already the bottom-up order the design
// describes - no separate sort table to maintain.
function groupByLayer(values: EffectiveConfigValue[]): [number, EffectiveConfigValue[]][] {
  const byLayer = new Map<number, EffectiveConfigValue[]>()
  for (const v of values) {
    const layer = v.origin?.layer ?? ConfigLayer.UNSPECIFIED
    const group = byLayer.get(layer)
    if (group) {
      group.push(v)
    } else {
      byLayer.set(layer, [v])
    }
  }
  return [...byLayer.entries()].sort((a, b) => a[0] - b[0])
}

// Known Talos service names (node.proto's own comment); "kernel" is this screen's own sentinel for the empty-string
// wire value (Talos's Dmesg log) - Select can't use "" as a real item
// value, since empty string is its own placeholder-vs-selected signal.
const LOG_SOURCES = [
  { value: 'kernel', label: 'Kernel (dmesg)', wire: '' },
  { value: 'kubelet', label: 'kubelet', wire: 'kubelet' },
  { value: 'etcd', label: 'etcd', wire: 'etcd' },
  { value: 'trustd', label: 'trustd', wire: 'trustd' },
  { value: 'apid', label: 'apid', wire: 'apid' },
  { value: 'machined', label: 'machined', wire: 'machined' },
]

function LogsSection({ namespace, name }: { namespace: string; name: string }) {
  const [source, setSource] = useState('kernel')
  const [lines, setLines] = useState<StreamNodeLogsResponse[]>([])
  const [error, setError] = useState<ErrorShape | null>(null)
  const [reconnecting, setReconnecting] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const service = LOG_SOURCES.find((s) => s.value === source)?.wire ?? ''

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false
    setLines([])
    setError(null)
    setReconnecting(false)

    async function run() {
      const call = (resumeToken: string) => {
        setReconnecting(resumeToken !== '')
        return nodeClient.streamNodeLogs(
          { machines: [{ namespace, name }], resumeToken, service },
          { signal: controller.signal },
        )
      }
      try {
        for await (const line of streamWithResume(call, (l) => l.resumeToken)) {
          if (cancelled) return
          setReconnecting(false)
          setLines((prev) => [...prev.slice(-499), line])
        }
      } catch (err) {
        if (!cancelled) setError(describeError(err))
      }
    }

    run()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [namespace, name, service])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [lines.length])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Logs
          {reconnecting && <Badge variant="outline">reconnecting…</Badge>}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div class="mb-2 w-48">
          <Select value={source} onValueChange={setSource}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {LOG_SOURCES.map((s) => (
                <SelectItem key={s.value} value={s.value}>
                  {s.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {error && <ErrorAlert {...error} />}
        <div class="max-h-96 overflow-y-auto rounded-md bg-muted/40 p-2 font-mono text-xs">
          {lines.length === 0 && !error && <p class="text-muted-foreground">Waiting for log lines…</p>}
          {lines.map((l, i) => (
            <div key={i}>
              <span class="text-muted-foreground">{formatTimestamp(l.time)}</span> {l.message}
            </div>
          ))}
          <div ref={bottomRef} />
        </div>
      </CardContent>
    </Card>
  )
}

function Field({ label, value }: { label: string; value: string | undefined }) {
  return (
    <div>
      <dt class="text-xs text-muted-foreground">{label}</dt>
      <dd>{value || '-'}</dd>
    </div>
  )
}
