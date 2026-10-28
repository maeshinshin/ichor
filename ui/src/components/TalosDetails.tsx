import { useEffect, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { Section } from '@/components/Section'
import type { GetNodeTalosDetailsResponse } from '@/gen/ichor/v1alpha1/talosdetails_pb'
import { nodeClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { display, formatBytes } from '@/lib/format'
import { formatSpeed, serviceTone, sortServices, visibleVolumes } from '@/lib/talosDetails'
import { useT } from '@/lib/useT'

// Read-only Talos details of one machine: one call, five
// independently failing parts. The node page's "Storage & network" tab renders
// part="storage" (disks, network), its "Talos" tab part="talos" (system,
// services, extensions); each tab makes its own call. A part that failed shows its error
// inline and the rest still render. Tables scroll inside their own container.
export function TalosDetails({ namespace, name, part }: { namespace: string; name: string; part: 'storage' | 'talos' }) {
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

  if (error) return <ErrorAlert {...error} />
  if (!data) return <p class="text-muted-foreground">{t('common.loading')}</p>
  if (part === 'storage') {
    return (
      <>
        <StorageSection data={data} />
        <NetworkSection data={data} />
      </>
    )
  }
  return (
    <>
      <SystemSection data={data} />
      <ServicesSection data={data} />
      <ExtensionsSection data={data} />
    </>
  )
}

function Failed({ error, children }: { error: string; children: ComponentChildren }) {
  const t = useT()
  if (!error) return <>{children}</>
  return (
    <Alert variant="destructive">
      <AlertDescription>
        {t('talos.sectionFailed')} <span class="break-words font-mono text-xs">{error}</span>
      </AlertDescription>
    </Alert>
  )
}

function list(items: string[]): string {
  return items.length ? items.join(', ') : ''
}

function SystemSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const s = data.system
  return (
    <Section id="talos" title={t('talos.system')}>
      {data.maintenanceMode && (
        <Alert>
          <AlertDescription>{t('talos.maintenance')}</AlertDescription>
        </Alert>
      )}
      {s && (
        <Failed error={s.error}>
          <Fields>
            <Field label={t('talos.version')} value={s.version} />
            <Field label={t('talos.arch')} value={s.arch} />
            <Field label={t('talos.platform')} value={[s.platform, s.platformMode].filter(Boolean).join(' / ')} />
            <Field label={t('talos.stage')} value={s.stage} />
            <Field label={t('talos.ready')}>
              <Badge variant={s.ready ? 'secondary' : 'outline'}>{s.ready ? t('common.yes') : t('common.no')}</Badge>
            </Field>
            <Field label={t('talos.hostname')} value={[s.hostname, s.domainname].filter(Boolean).join('.')} />
          </Fields>
          {s.unmetConditions.length > 0 && (
            <div>
              <p class="text-xs text-muted-foreground">{t('talos.unmet')}</p>
              <ul>
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
        </Failed>
      )}
    </Section>
  )
}

function NetworkSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const n = data.network
  if (!n) return null
  return (
    <Section id="network" title={t('node.network')}>
      <Failed error={n.error}>
        <Fields>
          <Field label={t('talos.addresses')} value={list(n.addresses)} />
          <Field label={t('talos.gateways')} value={list(n.defaultGateways)} />
          <Field label={t('talos.dns')} value={list(n.dnsServers)} />
          <Field label={t('talos.searchDomains')} value={list(n.searchDomains)} />
          <Field label={t('talos.timeServers')} value={list(n.timeServers)} />
        </Fields>
        {n.links.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('node.colInterface')}</TableHead>
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
                  <TableCell className="font-mono text-xs">{display(l.mac)}</TableCell>
                  <TableCell>
                    <Badge variant={l.linkUp ? 'secondary' : 'outline'}>{l.operationalState || (l.linkUp ? t('node.up') : t('node.down'))}</Badge>
                  </TableCell>
                  <TableCell>{l.mtu || '-'}</TableCell>
                  <TableCell>{formatSpeed(l.speedMbit)}</TableCell>
                  <TableCell>{display(l.kind)}</TableCell>
                  <TableCell>{display(l.driver)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Failed>
    </Section>
  )
}

function StorageSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const [showAll, setShowAll] = useState(false)
  const s = data.storage
  if (!s) return null
  const volumes = visibleVolumes(s.volumes, showAll)
  return (
    <Section id="storage" title={t('node.disks')}>
      <Failed error={s.error}>
        {s.disks.length === 0 ? (
          <p class="text-muted-foreground">{t('talos.none')}</p>
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
                  <TableCell>{display(d.model)}</TableCell>
                  <TableCell className="font-mono text-xs">{display(d.serial)}</TableCell>
                  <TableCell>{formatBytes(d.sizeBytes)}</TableCell>
                  <TableCell>{display(d.type)}</TableCell>
                  <TableCell className="space-x-1">
                    {d.systemDisk && <Badge variant="secondary">{t('talos.installDisk')}</Badge>}
                    {d.readOnly && <Badge variant="outline">{t('talos.readOnly')}</Badge>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="text-sm font-medium">{t('talos.volumes')}</h3>
          <Button size="sm" variant="outline" onClick={() => setShowAll(!showAll)}>
            {showAll ? t('talos.volumesDiskOnly') : t('talos.volumesShowAll', { n: s.volumes.length })}
          </Button>
        </div>
        {volumes.length === 0 ? (
          <p class="text-muted-foreground">{t('talos.none')}</p>
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
              {volumes.map((v) => (
                <TableRow key={v.id}>
                  <TableCell className="font-mono text-xs">{v.id}</TableCell>
                  <TableCell>{display(v.type)}</TableCell>
                  <TableCell>
                    <Badge variant={v.errorMessage ? 'destructive' : 'outline'}>{display(v.phase)}</Badge>
                    {v.errorMessage && <span class="ml-1 break-words text-xs text-destructive">{v.errorMessage}</span>}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{display(v.mountLocation)}</TableCell>
                  <TableCell>{formatBytes(v.sizeBytes)}</TableCell>
                  <TableCell>
                    {display(v.filesystem)} {v.encrypted && <Badge variant="outline">{t('talos.encrypted')}</Badge>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Failed>
    </Section>
  )
}

function ServicesSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const s = data.services
  if (!s) return null
  return (
    <Section id="services" title={t('talos.services')}>
      <Failed error={s.error}>
        {s.services.length === 0 ? (
          <p class="text-muted-foreground">{t('talos.none')}</p>
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
      </Failed>
    </Section>
  )
}

function ExtensionsSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const t = useT()
  const e = data.extensions
  if (!e) return null
  return (
    <Section id="extensions" title={t('talos.extensions')}>
      <Failed error={e.error}>
        {e.extensions.length === 0 ? (
          <p class="text-muted-foreground">{t('talos.noExtensions')}</p>
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
                  <TableCell>{display(x.version)}</TableCell>
                  <TableCell>{display(x.author)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Failed>
    </Section>
  )
}
