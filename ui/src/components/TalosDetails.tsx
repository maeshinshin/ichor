import { useState } from 'preact/hooks'
import { CircleXIcon, InfoIcon } from 'lucide-preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CodeBlock, CodeBlockContent, CodeBlockLine } from '@/components/ui/code-block'
import { Item, ItemContent, ItemDescription, ItemGroup, ItemSeparator, ItemTitle } from '@/components/ui/item'
import { TableCell, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { Failed, Grid } from '@/components/PageStates'
import { SectionCard, SectionSkeleton } from '@/components/SectionCard'
import { ToneBadge } from '@/components/ToneBadge'
import type { GetNodeTalosDetailsResponse } from '@/gen/ichor/v1alpha1/talosdetails_pb'
import { nodeClient } from '@/lib/connect'
import { display, formatBytes } from '@/lib/format'
import { formatSpeed, serviceStatus, sortServices, visibleVolumes } from '@/lib/talosDetails'
import { useLoad } from '@/lib/useLoad'
import { t } from '@/lib/i18n'

// Read-only Talos details of one machine: one call, five
// independently failing parts. The node page's "Storage & network" tab renders
// part="storage" (disks, network), its "Talos" tab part="talos" (system,
// services, extensions); each tab makes its own call. A part that failed shows its error
// inline and the rest still render. Tables scroll inside their own container.
export function TalosDetails({ namespace, name, part }: { namespace: string; name: string; part: 'storage' | 'talos' }) {
  const { data, error } = useLoad(() => nodeClient.getNodeTalosDetails({ machine: { namespace, name } }), [namespace, name])

  if (error) return <ErrorAlert {...error} />
  if (!data) return <SectionSkeleton label={t('common.loading')} />
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

function list(items: string[]): string {
  return items.length ? items.join(', ') : ''
}

function SystemSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const s = data.system
  return (
    <SectionCard id="talos" title={t('talos.system')}>
      {data.maintenanceMode && (
        <Alert>
          <InfoIcon />
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
              <ToneBadge tone={s.ready ? 'good' : 'neutral'}>{s.ready ? t('common.yes') : t('common.no')}</ToneBadge>
            </Field>
            <Field label={t('talos.hostname')} value={[s.hostname, s.domainname].filter(Boolean).join('.')} />
            {s.kernelLockdown !== '' && (
              <>
                <Field label={t('talos.secureBoot')}>
                  <ToneBadge tone={s.secureBoot ? 'good' : 'neutral'}>{s.secureBoot ? t('common.yes') : t('common.no')}</ToneBadge>
                </Field>
                <Field label={t('talos.uki')}>
                  <ToneBadge tone={s.bootedWithUki ? 'good' : 'neutral'}>{s.bootedWithUki ? t('common.yes') : t('common.no')}</ToneBadge>
                </Field>
                <Field label={t('talos.signedModules')}>
                  <ToneBadge tone={s.moduleSignatureEnforced ? 'good' : 'neutral'}>
                    {s.moduleSignatureEnforced ? t('common.yes') : t('common.no')}
                  </ToneBadge>
                </Field>
                <Field label={t('talos.lockdown')} value={s.kernelLockdown} />
                <Field label={t('talos.pcrKey')} value={s.pcrSigningKeyFingerprint} />
              </>
            )}
          </Fields>
          {s.unmetConditions.length > 0 && (
            <SectionCard size="sm" title={t('talos.unmet')}>
              <ItemGroup>
                {s.unmetConditions.map((c, i) => (
                  <div key={c.name}>
                    {i > 0 && <ItemSeparator />}
                    <Item>
                      <ItemContent>
                        <ItemTitle>{c.name}</ItemTitle>
                        {c.reason && <ItemDescription>{c.reason}</ItemDescription>}
                      </ItemContent>
                    </Item>
                  </div>
                ))}
              </ItemGroup>
            </SectionCard>
          )}
          {s.kernelArgs.length > 0 && (
            <SectionCard size="sm" title={t('talos.kernelArgs')} description={t('talos.kernelArgsNote')}>
              <CodeBlock size="sm">
                <CodeBlockContent wrap>
                  <CodeBlockLine>{s.kernelArgs.join(' ')}</CodeBlockLine>
                </CodeBlockContent>
              </CodeBlock>
            </SectionCard>
          )}
        </Failed>
      )}
    </SectionCard>
  )
}

function NetworkSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const n = data.network
  if (!n) return null
  return (
    <SectionCard id="network" title={t('node.network')}>
      <Failed error={n.error}>
        <Fields>
          <Field label={t('talos.addresses')} value={list(n.addresses)} />
          <Field label={t('talos.gateways')} value={list(n.defaultGateways)} />
          <Field label={t('talos.dns')} value={list(n.dnsServers)} />
          <Field label={t('talos.searchDomains')} value={list(n.searchDomains)} />
          <Field label={t('talos.timeServers')} value={list(n.timeServers)} />
        </Fields>
        {n.links.length > 0 && (
          <Grid heads={[t('node.colInterface'), t('node.colMac'), t('talos.colState'), t('talos.colMtu'), t('talos.colSpeed'), t('talos.colKind'), t('talos.colDriver')]} phoneHidden={[1, 3, 4, 5, 6]}>
              {n.links.map((l) => (
                <TableRow key={l.name}>
                  <TableCell className="font-mono max-md:whitespace-normal max-md:wrap-anywhere">
                    {l.name}
                    <div class="font-sans text-muted-foreground md:hidden">{display(l.mac)}</div>
                    <div class="font-sans text-muted-foreground md:hidden">
                      {l.mtu || '-'} · {formatSpeed(l.speedMbit)} · {display(l.kind)} · {display(l.driver)}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono max-md:hidden">{display(l.mac)}</TableCell>
                  <TableCell>
                    <ToneBadge tone={l.linkUp ? 'good' : 'neutral'}>{l.operationalState || (l.linkUp ? t('node.up') : t('node.down'))}</ToneBadge>
                  </TableCell>
                  <TableCell className="max-md:hidden">{l.mtu || '-'}</TableCell>
                  <TableCell className="max-md:hidden">{formatSpeed(l.speedMbit)}</TableCell>
                  <TableCell className="max-md:hidden">{display(l.kind)}</TableCell>
                  <TableCell className="max-md:hidden">{display(l.driver)}</TableCell>
                </TableRow>
              ))}
          </Grid>
        )}
      </Failed>
    </SectionCard>
  )
}

function StorageSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const [showAll, setShowAll] = useState(false)
  const s = data.storage
  if (!s) return null
  const volumes = visibleVolumes(s.volumes, showAll)
  const failedVolumes = volumes.filter((v) => v.errorMessage)
  return (
    <>
      <SectionCard id="storage" title={t('node.disks')}>
        <Failed error={s.error}>
          <Grid heads={[t('talos.colDevice'), t('talos.colModel'), t('talos.colSerial'), t('node.colSize'), t('talos.colType'), '']} phoneHidden={[1, 2, 4]} empty={s.disks.length === 0}>
                {s.disks.map((d) => (
                  <TableRow key={d.id}>
                    <TableCell className="font-mono max-md:whitespace-normal max-md:wrap-anywhere">
                      {d.devPath || d.id}
                      <div class="font-sans text-muted-foreground md:hidden">
                        {display(d.model)} · {display(d.type)}
                      </div>
                      <div class="text-muted-foreground md:hidden">{display(d.serial)}</div>
                    </TableCell>
                    <TableCell className="max-md:hidden">{display(d.model)}</TableCell>
                    <TableCell className="font-mono max-md:hidden">{display(d.serial)}</TableCell>
                    <TableCell>{formatBytes(d.sizeBytes)}</TableCell>
                    <TableCell className="max-md:hidden">{display(d.type)}</TableCell>
                    <TableCell>
                      <div class="flex flex-wrap gap-1">
                        {d.systemDisk && <Badge variant="secondary">{t('talos.installDisk')}</Badge>}
                        {d.readOnly && <Badge variant="outline">{t('talos.readOnly')}</Badge>}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
          </Grid>
        </Failed>
      </SectionCard>
      {!s.error && (
      <SectionCard
        id="volumes"
        title={t('talos.volumes')}
        actions={
          <Button size="sm" variant="outline" onClick={() => setShowAll(!showAll)}>
            {showAll ? t('talos.volumesDiskOnly') : t('talos.volumesShowAll', { n: s.volumes.length })}
          </Button>
        }
      >
          <Grid heads={[t('talos.colName'), t('talos.colType'), t('talos.colPhase'), t('talos.colMount'), t('node.colSize'), t('talos.colFilesystem')]} phoneHidden={[1, 3, 5]} empty={volumes.length === 0}>
                {volumes.map((v) => (
                  <TableRow key={v.id}>
                    <TableCell className="font-mono max-md:whitespace-normal max-md:wrap-anywhere">
                      {v.id}
                      <div class="font-sans text-muted-foreground md:hidden">
                        {display(v.type)} · {display(v.filesystem)} {v.encrypted && t('talos.encrypted')}
                      </div>
                      <div class="text-muted-foreground md:hidden">{display(v.mountLocation)}</div>
                    </TableCell>
                    <TableCell className="max-md:hidden">{display(v.type)}</TableCell>
                    <TableCell>
                      <Badge variant={v.errorMessage ? 'destructive' : 'outline'}>{display(v.phase)}</Badge>
                    </TableCell>
                    <TableCell className="font-mono max-md:hidden">{display(v.mountLocation)}</TableCell>
                    <TableCell>{formatBytes(v.sizeBytes)}</TableCell>
                    <TableCell className="max-md:hidden">
                      {display(v.filesystem)} {v.encrypted && <Badge variant="outline">{t('talos.encrypted')}</Badge>}
                    </TableCell>
                  </TableRow>
                ))}
          </Grid>
          {failedVolumes.map((v) => (
            <Alert key={v.id} variant="destructive">
              <CircleXIcon />
              <AlertTitle>{v.id}</AlertTitle>
              <AlertDescription className="break-words">{v.errorMessage}</AlertDescription>
            </Alert>
          ))}
      </SectionCard>
      )}
    </>
  )
}

function ServicesSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const s = data.services
  if (!s) return null
  return (
    <SectionCard id="services" title={t('talos.services')}>
      <Failed error={s.error}>
        <Grid heads={[t('talos.colService'), t('talos.colState')]} empty={s.services.length === 0}>
              {sortServices(s.services).map((svc) => {
                const status = serviceStatus(svc)
                return (
                  <TableRow key={svc.id}>
                    <TableCell className="font-mono">{svc.id}</TableCell>
                    <TableCell>
                      <ToneBadge tone={status.tone}>{status.labelKey ? t(status.labelKey) : status.label}</ToneBadge>
                      {status.message && <span className="break-words text-muted-foreground"> {status.message}</span>}
                    </TableCell>
                  </TableRow>
                )
              })}
        </Grid>
      </Failed>
    </SectionCard>
  )
}

function ExtensionsSection({ data }: { data: GetNodeTalosDetailsResponse }) {
  const e = data.extensions
  if (!e) return null
  return (
    <SectionCard id="extensions" title={t('talos.extensions')}>
      <Failed error={e.error}>
        <Grid heads={[t('talos.colName'), t('talos.colVersion'), t('talos.colAuthor')]} phoneHidden={[1, 2]} empty={e.extensions.length === 0} none="talos.noExtensions">
              {e.extensions.map((x) => (
                <TableRow key={x.name}>
                  <TableCell className="max-md:whitespace-normal max-md:wrap-anywhere">
                    {x.name}
                    <div class="text-muted-foreground md:hidden">{display(x.version)}</div>
                    <div class="text-muted-foreground md:hidden">{display(x.author)}</div>
                  </TableCell>
                  <TableCell className="max-md:hidden">{display(x.version)}</TableCell>
                  <TableCell className="max-md:hidden">{display(x.author)}</TableCell>
                </TableRow>
              ))}
        </Grid>
      </Failed>
    </SectionCard>
  )
}
