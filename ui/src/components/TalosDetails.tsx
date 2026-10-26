import { useEffect, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { GetNodeTalosDetailsResponse } from '@/gen/ichor/v1alpha1/talosdetails_pb'
import { nodeClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { formatBytes } from '@/lib/format'
import { formatSpeed, serviceTone, sortServices, timeSyncLabel } from '@/lib/talosDetails'
import { useT } from '@/lib/useT'

// Read-only Talos details of one machine (docs/adr/0073). One call, five
// independently failing sections: a failed section shows its own error inline
// and the rest still render. Tables scroll inside their own container, so the
// cards stack on a phone.
export function TalosDetails({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [data, setData] = useState<GetNodeTalosDetailsResponse | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)

  useEffect(() => {
    let cancelled = false
    setData(null)
    setError(null)
    nodeClient.getNodeTalosDetails({ machine: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setData(res)
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
        <CardTitle>{t('talos.title')}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p class="text-sm text-muted-foreground">{t('talos.intro')}</p>
        {error && <ErrorAlert {...error} />}
        {!error && !data && <p class="text-sm text-muted-foreground">{t('common.loading')}</p>}
        {data?.maintenanceMode && (
          <Alert>
            <AlertDescription>{t('talos.maintenance')}</AlertDescription>
          </Alert>
        )}
        {data && (
          <>
            <SystemSection data={data} />
            <NetworkSection data={data} />
            <StorageSection data={data} />
            <ServicesSection data={data} />
            <ExtensionsSection data={data} />
          </>
        )}
      </CardContent>
    </Card>
  )
}

function Sub({ title, error, children }: { title: string; error: string; children: ComponentChildren }) {
  const t = useT()
  return (
    <section class="space-y-2 rounded-md border p-3">
      <h4 class="text-sm font-medium">{title}</h4>
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>
            {t('talos.sectionFailed')} <span class="break-words font-mono text-xs">{error}</span>
          </AlertDescription>
        </Alert>
      ) : (
        children
      )}
    </section>
  )
}

function Field({ label, value }: { label: string; value: ComponentChildren }) {
  return (
    <div class="min-w-0">
      <dt class="text-xs text-muted-foreground">{label}</dt>
      <dd class="break-words">{value || '-'}</dd>
    </div>
  )
}

const FIELDS = 'grid grid-cols-1 gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3'

function list(items: string[]): string {
  return items.length ? items.join(', ') : ''
}

function SystemSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const s = data.system
  if (!s) return null
  return (
    <Sub title={t('talos.system')} error={s.error}>
      <dl class={FIELDS}>
        <Field label={t('talos.version')} value={s.version} />
        <Field label={t('talos.arch')} value={s.arch} />
        <Field label={t('talos.platform')} value={[s.platform, s.platformMode].filter(Boolean).join(' / ')} />
        <Field label={t('talos.stage')} value={s.stage} />
        <Field
          label={t('talos.ready')}
          value={<Badge variant={s.ready ? 'secondary' : 'outline'}>{s.ready ? t('common.yes') : t('common.no')}</Badge>}
        />
        <Field label={t('talos.hostname')} value={[s.hostname, s.domainname].filter(Boolean).join('.')} />
        <Field label={t('talos.k8sNodename')} value={s.kubernetesNodename} />
      </dl>
      {s.unmetConditions.length > 0 && (
        <div>
          <p class="text-xs text-muted-foreground">{t('talos.unmet')}</p>
          <ul class="text-sm">
            {s.unmetConditions.map((c) => (
              <li key={c.name} class="break-words">
                <span class="font-mono text-xs">{c.name}</span> {c.reason && <span class="text-muted-foreground">{c.reason}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      {s.kernelArgs.length > 0 && (
        <div>
          <p class="text-xs text-muted-foreground">
            {t('talos.kernelArgs')} - {t('talos.kernelArgsNote')}
          </p>
          <p class="break-all rounded-md bg-muted/40 p-2 font-mono text-xs">{s.kernelArgs.join(' ')}</p>
        </div>
      )}
    </Sub>
  )
}

function NetworkSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const n = data.network
  if (!n) return null
  return (
    <Sub title={t('talos.networkSection')} error={n.error}>
      <dl class={FIELDS}>
        <Field label={t('talos.addresses')} value={list(n.addresses)} />
        <Field label={t('talos.gateways')} value={list(n.defaultGateways)} />
        <Field label={t('talos.routes')} value={String(n.routeCount)} />
        <Field label={t('talos.dns')} value={list(n.dnsServers)} />
        <Field label={t('talos.searchDomains')} value={list(n.searchDomains)} />
        <Field label={t('talos.timeServers')} value={list(n.timeServers)} />
        <Field label={t('talos.timeSync')} value={timeSyncLabel(n.timeSynced, n.timeSyncDisabled)} />
      </dl>
      {n.links.length > 0 && (
        <>
          <p class="text-xs text-muted-foreground">{t('talos.links')}</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('talos.colName')}</TableHead>
                <TableHead>{t('node.colMac')}</TableHead>
                <TableHead>{t('talos.colState')}</TableHead>
                <TableHead>{t('talos.colMtu')}</TableHead>
                <TableHead>{t('talos.colSpeed')}</TableHead>
                <TableHead>{t('talos.colKind')}</TableHead>
                <TableHead>{t('talos.colDriver')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {n.links.map((l) => (
                <TableRow key={l.name}>
                  <TableCell className="font-mono text-xs">{l.name}</TableCell>
                  <TableCell className="font-mono text-xs">{l.mac || '-'}</TableCell>
                  <TableCell>
                    <Badge variant={l.linkUp ? 'secondary' : 'outline'}>{l.operationalState || (l.linkUp ? t('node.up') : t('node.down'))}</Badge>
                  </TableCell>
                  <TableCell>{l.mtu || '-'}</TableCell>
                  <TableCell>{formatSpeed(l.speedMbit)}</TableCell>
                  <TableCell>{l.kind || '-'}</TableCell>
                  <TableCell>{l.driver || '-'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
    </Sub>
  )
}

function StorageSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const s = data.storage
  if (!s) return null
  return (
    <Sub title={t('talos.storage')} error={s.error}>
      <p class="text-xs text-muted-foreground">{t('talos.disks')}</p>
      {s.disks.length === 0 ? (
        <p class="text-sm text-muted-foreground">{t('talos.none')}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('talos.colDevice')}</TableHead>
              <TableHead>{t('talos.colModel')}</TableHead>
              <TableHead>{t('talos.colSerial')}</TableHead>
              <TableHead>{t('node.colSize')}</TableHead>
              <TableHead>{t('talos.colType')}</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {s.disks.map((d) => (
              <TableRow key={d.id}>
                <TableCell className="font-mono text-xs">{d.devPath || d.id}</TableCell>
                <TableCell>{d.model || '-'}</TableCell>
                <TableCell className="font-mono text-xs">{d.serial || '-'}</TableCell>
                <TableCell>{formatBytes(d.sizeBytes)}</TableCell>
                <TableCell>{d.type || '-'}</TableCell>
                <TableCell className="space-x-1">
                  {d.systemDisk && <Badge variant="secondary">{t('talos.installDisk')}</Badge>}
                  {d.readOnly && <Badge variant="outline">{t('talos.readOnly')}</Badge>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <p class="text-xs text-muted-foreground">{t('talos.volumes')}</p>
      {s.volumes.length === 0 ? (
        <p class="text-sm text-muted-foreground">{t('talos.none')}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('talos.colName')}</TableHead>
              <TableHead>{t('talos.colType')}</TableHead>
              <TableHead>{t('talos.colPhase')}</TableHead>
              <TableHead>{t('talos.colMount')}</TableHead>
              <TableHead>{t('node.colSize')}</TableHead>
              <TableHead>{t('talos.colFilesystem')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {s.volumes.map((v) => (
              <TableRow key={v.id}>
                <TableCell className="font-mono text-xs">{v.id}</TableCell>
                <TableCell>{v.type || '-'}</TableCell>
                <TableCell>
                  <Badge variant={v.errorMessage ? 'destructive' : 'outline'}>{v.phase || '-'}</Badge>
                  {v.errorMessage && <span class="ml-1 break-words text-xs text-destructive">{v.errorMessage}</span>}
                </TableCell>
                <TableCell className="font-mono text-xs">{v.mountLocation || '-'}</TableCell>
                <TableCell>{formatBytes(v.sizeBytes)}</TableCell>
                <TableCell>
                  {v.filesystem || '-'} {v.encrypted && <Badge variant="outline">{t('talos.encrypted')}</Badge>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Sub>
  )
}

function ServicesSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const s = data.services
  if (!s) return null
  return (
    <Sub title={t('talos.services')} error={s.error}>
      {s.services.length === 0 ? (
        <p class="text-sm text-muted-foreground">{t('talos.none')}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('talos.colService')}</TableHead>
              <TableHead>{t('talos.colState')}</TableHead>
              <TableHead>{t('talos.colHealth')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortServices(s.services).map((svc) => {
              const tone = serviceTone(svc)
              return (
                <TableRow key={svc.id}>
                  <TableCell className="font-mono text-xs">{svc.id}</TableCell>
                  <TableCell>
                    <Badge variant={tone === 'bad' ? 'destructive' : tone === 'ok' ? 'secondary' : 'outline'}>{svc.state}</Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {svc.healthUnknown ? '-' : svc.healthy ? t('common.yes') : t('common.no')}
                    {svc.healthMessage && <span class="ml-1 break-words">{svc.healthMessage}</span>}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </Sub>
  )
}

function ExtensionsSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const e = data.extensions
  if (!e) return null
  return (
    <Sub title={t('talos.extensions')} error={e.error}>
      {e.extensions.length === 0 ? (
        <p class="text-sm text-muted-foreground">{t('talos.noExtensions')}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('talos.colName')}</TableHead>
              <TableHead>{t('talos.colVersion')}</TableHead>
              <TableHead>{t('talos.colAuthor')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {e.extensions.map((x) => (
              <TableRow key={x.name}>
                <TableCell>{x.name}</TableCell>
                <TableCell>{x.version || '-'}</TableCell>
                <TableCell>{x.author || '-'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Sub>
  )
}
