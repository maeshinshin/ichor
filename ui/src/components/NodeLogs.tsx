import { Component, type ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { StreamNodeLogsResponse } from '@/gen/ichor/v1alpha1/node_pb'
import { nodeClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { formatTimestamp } from '@/lib/format'
import { createBatcher, type Entry, LogBuffer, truncateMessage } from '@/lib/logBuffer'
import { streamWithResume } from '@/lib/resumableStream'
import { useT } from '@/lib/useT'
import type { Key } from '@/lib/i18n'

// The viewer keeps the newest MAX_LINES lines, appends them in
// batches every FLUSH_MS, and shows at most MAX_CHARS of one message until
// the person expands it.
const MAX_LINES = 2000
const FLUSH_MS = 100
const MAX_CHARS = 1000

// Known Talos service names (node.proto's own comment); "kernel" is this screen's own sentinel for the empty-string
// wire value (Talos's Dmesg log) - Select can't use "" as a real item
// value, since empty string is its own placeholder-vs-selected signal.
const LOG_SOURCES: { value: string; label: Key | string; wire: string }[] = [
  { value: 'kernel', label: 'node.logKernel', wire: '' },
  { value: 'kubelet', label: 'kubelet', wire: 'kubelet' },
  { value: 'etcd', label: 'etcd', wire: 'etcd' },
  { value: 'trustd', label: 'trustd', wire: 'trustd' },
  { value: 'apid', label: 'apid', wire: 'apid' },
  { value: 'machined', label: 'machined', wire: 'machined' },
]

type ErrorShape = { title: string; detail: string }

// A render error in the log list must not take the whole page down
// (preact unmounts the tree on an uncaught render error).
class LogBoundary extends Component<{ children: ComponentChildren }, { error: Error | null }> {
  state = { error: null as Error | null }

  componentDidCatch(error: Error) {
    this.setState({ error })
  }

  render() {
    if (this.state.error) return <ErrorAlert {...describeError(this.state.error)} />
    return this.props.children
  }
}

export function NodeLogs({ namespace, name }: { namespace: string; name: string }) {
  return (
    <LogBoundary>
      <LogsSection namespace={namespace} name={name} />
    </LogBoundary>
  )
}

function LogsSection({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [source, setSource] = useState('kernel')
  const [entries, setEntries] = useState<Entry<StreamNodeLogsResponse>[]>([])
  const [error, setError] = useState<ErrorShape | null>(null)
  const [reconnecting, setReconnecting] = useState(false)
  const [paused, setPaused] = useState(false)
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set())
  const buffer = useRef(new LogBuffer<StreamNodeLogsResponse>(MAX_LINES))
  const pausedRef = useRef(false)
  const atBottom = useRef(true)
  const listRef = useRef<HTMLDivElement>(null)
  const service = LOG_SOURCES.find((s) => s.value === source)?.wire ?? ''

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false
    const buf = buffer.current
    buf.clear()
    setEntries([])
    setExpanded(new Set())
    setError(null)
    setReconnecting(false)

    const batcher = createBatcher<StreamNodeLogsResponse>(
      (lines) => {
        buf.push(lines)
        if (!pausedRef.current) setEntries(buf.entries())
      },
      (cb) => {
        const handle = setTimeout(cb, FLUSH_MS)
        return () => clearTimeout(handle)
      },
    )

    async function run() {
      const call = (resumeToken: string) => {
        setReconnecting(resumeToken !== '')
        return nodeClient.streamNodeLogs(
          { machines: [{ namespace, name }], resumeToken, service },
          { signal: controller.signal },
        )
      }
      try {
        for await (const line of streamWithResume(call, (l) => l.resumeToken, '', { signal: controller.signal })) {
          if (cancelled) return
          setReconnecting(false)
          batcher.push(line)
        }
      } catch (err) {
        if (!cancelled) setError(describeError(err))
      }
    }

    run()
    return () => {
      cancelled = true
      controller.abort()
      batcher.cancel()
      buf.clear()
    }
  }, [namespace, name, service])

  // Follow the tail only while the person is at the bottom.
  useEffect(() => {
    const el = listRef.current
    if (el && atBottom.current) el.scrollTop = el.scrollHeight
  }, [entries])

  function onScroll() {
    const el = listRef.current
    if (el) atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }

  function togglePause() {
    const next = !pausedRef.current
    pausedRef.current = next
    setPaused(next)
    if (!next) setEntries(buffer.current.entries())
  }

  function toggleExpanded(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          {t('node.logs')}
          {reconnecting && <Badge variant="outline">{t('node.reconnecting')}</Badge>}
          {paused && <Badge variant="outline">{t('node.logPaused')}</Badge>}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div class="mb-2 flex items-center gap-2">
          <div class="w-48">
            <Select value={source} onValueChange={setSource}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LOG_SOURCES.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {s.label.startsWith('node.') ? t(s.label as Key) : s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button variant="outline" size="sm" onClick={togglePause}>
            {paused ? t('node.logResume') : t('node.logPause')}
          </Button>
        </div>
        {error && <ErrorAlert {...error} />}
        <div
          ref={listRef}
          onScroll={onScroll}
          class="max-h-96 overflow-y-auto rounded-md bg-muted/40 p-2 font-mono text-xs"
        >
          {entries.length === 0 && !error && <p class="text-muted-foreground">{t('node.logWaiting')}</p>}
          {entries.map((e) => {
            const open = expanded.has(e.id)
            const { text, truncated } = open ? { text: e.item.message, truncated: false } : truncateMessage(e.item.message, MAX_CHARS)
            const canCollapse = open && e.item.message.length > MAX_CHARS
            return (
              <div key={e.id} class="break-words whitespace-pre-wrap">
                <span class="text-muted-foreground">{formatTimestamp(e.item.time)}</span> {text}
                {(truncated || canCollapse) && (
                  <>
                    {truncated && '… '}
                    <button type="button" class="text-primary underline" onClick={() => toggleExpanded(e.id)}>
                      {truncated ? t('node.logExpand') : t('node.logCollapse')}
                    </button>
                  </>
                )}
              </div>
            )
          })}
        </div>
      </CardContent>
    </Card>
  )
}
