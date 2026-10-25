import { useEffect, useRef, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>Effective config</CardTitle>
      </CardHeader>
      <CardContent>
        {error && <ErrorAlert {...error} />}
        {!error && !values && <p class="text-sm text-muted-foreground">Loading…</p>}
        {values && values.length === 0 && <p class="text-sm text-muted-foreground">No values reported.</p>}
        {values && values.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Path</TableHead>
                <TableHead>Value</TableHead>
                <TableHead>Origin</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {values.map((v) => (
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
                  <TableCell className="text-xs text-muted-foreground">
                    {v.origin ? `${enumLabel(ConfigLayer, v.origin.layer)}: ${v.origin.source}` : '-'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function LogsSection({ namespace, name }: { namespace: string; name: string }) {
  const [lines, setLines] = useState<StreamNodeLogsResponse[]>([])
  const [error, setError] = useState<ErrorShape | null>(null)
  const [reconnecting, setReconnecting] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false
    setLines([])
    setError(null)
    setReconnecting(false)

    async function run() {
      const call = (resumeToken: string) => {
        setReconnecting(resumeToken !== '')
        return nodeClient.streamNodeLogs({ machines: [{ namespace, name }], resumeToken }, { signal: controller.signal })
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
  }, [namespace, name])

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
