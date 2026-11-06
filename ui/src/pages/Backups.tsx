import { useEffect, useState } from 'preact/hooks'
import { InboxIcon, TriangleAlertIcon } from 'lucide-preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { CodeBlock, CodeBlockActions, CodeBlockContent, CodeBlockCopyButton, CodeBlockHeader, CodeBlockLine, CodeBlockTitle } from '@/components/ui/code-block'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { SectionCard } from '@/components/SectionCard'
import { PageTitle } from '@/components/PageTitle'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ToneBadge } from '@/components/ToneBadge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RestoreDialog } from '@/components/RestoreDialog'
import { BackupNow } from '@/components/BackupNow'
import type { EtcdBackup } from '@/gen/ichor/v1alpha1/operation_pb'
import { destinationLabel, exampleBackupYaml, newestSnapshot, totalBytes } from '@/lib/backups'
import { operationClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { formatBytes, formatTimestamp } from '@/lib/format'
import { useCluster } from '@/lib/useCluster'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { mayStart } from '@/lib/actions'
import { t } from '@/lib/i18n'

// Backups: the cluster's TalosEtcdBackups, the snapshots they hold, and restore.
// A backup is created by applying a TalosEtcdBackup, and the controller takes the snapshots; Back up now only
// sets a request on it.
export function Backups() {
  const { cluster, loading, error: clusterError } = useCluster()
  const [backups, setBackups] = useState<EtcdBackup[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const ref = cluster?.ref
  // What the caller's role may do; a button it cannot use is not shown (the server checks again).
  const perms = useMyPermissions(undefined, ref)

  useEffect(() => {
    if (!ref) return
    let cancelled = false
    operationClient.listEtcdBackups({ cluster: ref }).then(
      (res) => !cancelled && setBackups(res.backups),
      (err) => !cancelled && setError(describeError(err)),
    )
    return () => {
      cancelled = true
    }
  }, [ref?.namespace, ref?.name])

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (error) return <ErrorAlert {...error} />
  if (loading || (ref && !backups)) return <PageSkeleton label={t('backups.loading')} />
  if (!ref || !backups) {
    return (
      <div>
        <PageTitle title={t('backups.title')} />
        <EmptyNote icon={InboxIcon}>{t('backups.noCluster')}</EmptyNote>
      </div>
    )
  }

  const all = backups.flatMap((b) => b.snapshots.map((s) => ({ backup: b, snap: s })))
  all.sort((a, b) => Number((b.snap.time?.seconds ?? 0n) - (a.snap.time?.seconds ?? 0n)) || a.snap.name.localeCompare(b.snap.name))
  const example = exampleBackupYaml(ref)

  return (
    <div class="space-y-4">
      <PageTitle
        title={t('backups.title')}
        description={ref.name}
        actions={backups.length > 0 && mayStart(perms, 'etcd-restore') ? <RestoreDialog cluster={ref} disabled={all.length === 0} /> : undefined}
      />
      {backups.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>{t('backups.emptyTitle')}</CardTitle>
            <CardDescription>{t('backups.emptyBody')}</CardDescription>
          </CardHeader>
          <CardContent>
            <CodeBlock size="sm">
              <CodeBlockHeader>
                <CodeBlockTitle>TalosEtcdBackup</CodeBlockTitle>
                <CodeBlockActions>
                  <CodeBlockCopyButton value={example} />
                </CodeBlockActions>
              </CodeBlockHeader>
              <CodeBlockContent>
                {example.split('\n').map((line, i) => (
                  <CodeBlockLine key={i}>{line}</CodeBlockLine>
                ))}
              </CodeBlockContent>
            </CodeBlock>
          </CardContent>
        </Card>
      ) : (
        <>
          <SectionCard title={t('backups.configs')} description={t('backups.hint')}>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('backups.colName')}</TableHead>
                  <TableHead className="hidden md:table-cell">{t('backups.colSchedule')}</TableHead>
                  <TableHead className="hidden xl:table-cell">{t('backups.colDestination')}</TableHead>
                  <TableHead className="hidden xl:table-cell">{t('backups.colRetention')}</TableHead>
                  <TableHead className="hidden md:table-cell">{t('backups.colLast')}</TableHead>
                  <TableHead className="hidden xl:table-cell">{t('backups.colSnapshots')}</TableHead>
                  <TableHead className="hidden xl:table-cell">{t('backups.colSize')}</TableHead>
                  <TableHead>{t('backups.colStatus')}</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {backups.map((b) => {
                  const last = newestSnapshot(b.snapshots)
                  const lastText = b.lastSuccessTime ? formatTimestamp(b.lastSuccessTime) : last ? formatTimestamp(last.time) : t('backups.never')
                  return (
                    <TableRow key={b.ref?.name}>
                      <TableCell className="font-medium whitespace-normal wrap-anywhere">
                        {b.ref?.name}
                        <div class="font-mono font-normal text-muted-foreground md:hidden">{b.schedule}</div>
                        <div class="font-normal text-muted-foreground md:hidden">{lastText}</div>
                        <div class="font-mono font-normal text-muted-foreground wrap-anywhere xl:hidden">{destinationLabel(b.destination)}</div>
                        <div class="font-normal text-muted-foreground xl:hidden">
                          {t('backups.keepLast', { count: b.keepLast })} · {b.snapshots.length} · {formatBytes(totalBytes(b.snapshots))}
                        </div>
                        <div class="font-normal text-muted-foreground">
                          {b.manualBackupPending
                            ? t('backups.manualPending')
                            : b.lastManualBackup
                              ? t('backups.manualDone', { time: formatTimestamp(b.lastManualBackup) })
                              : t('backups.manualNone')}
                        </div>
                      </TableCell>
                      <TableCell className="hidden font-mono md:table-cell">{b.schedule}</TableCell>
                      <TableCell className="hidden font-mono xl:table-cell">{destinationLabel(b.destination)}</TableCell>
                      <TableCell className="hidden xl:table-cell">{t('backups.keepLast', { count: b.keepLast })}</TableCell>
                      <TableCell className="hidden md:table-cell">{lastText}</TableCell>
                      <TableCell className="hidden xl:table-cell">{b.snapshots.length}</TableCell>
                      <TableCell className="hidden xl:table-cell">{formatBytes(totalBytes(b.snapshots))}</TableCell>
                      <TableCell>
                        {b.suspended ? (
                          <ToneBadge tone="neutral">{t('backups.suspended')}</ToneBadge>
                        ) : (
                          <ToneBadge tone={b.ready ? 'good' : 'warn'}>{b.ready ? t('backups.ok') : t('backups.stale')}</ToneBadge>
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        {perms?.updateBackups && <BackupNow backup={b} onChanged={(next) => setBackups(backups.map((x) => (x.ref?.name === next.ref?.name ? next : x)))} />}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
            {backups.map((b) =>
              b.lastError ? (
                <Alert key={b.ref?.name} variant="destructive">
                  <TriangleAlertIcon />
                  <AlertTitle>{t('backups.lastError')}</AlertTitle>
                  <AlertDescription className="break-words">{b.lastError}</AlertDescription>
                </Alert>
              ) : null,
            )}
          </SectionCard>
          <SectionCard title={t('backups.snapshots')} description={t('backups.restoreHint')}>
            {all.length === 0 ? (
              <EmptyNote icon={InboxIcon}>{t('backups.snapNone')}</EmptyNote>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('backups.snapTime')}</TableHead>
                    <TableHead className="hidden sm:table-cell">{t('backups.snapName')}</TableHead>
                    <TableHead className="hidden sm:table-cell">{t('backups.colName')}</TableHead>
                    <TableHead>{t('backups.colSize')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {all.map(({ backup, snap }) => (
                    <TableRow key={`${backup.ref?.name}/${snap.name}`}>
                      <TableCell className="max-sm:whitespace-normal">
                        {formatTimestamp(snap.time)}
                        <div class="font-mono text-muted-foreground wrap-anywhere sm:hidden">{snap.name}</div>
                        <div class="text-muted-foreground wrap-anywhere sm:hidden">{backup.ref?.name}</div>
                      </TableCell>
                      <TableCell className="hidden font-mono sm:table-cell">{snap.name}</TableCell>
                      <TableCell className="hidden sm:table-cell">{backup.ref?.name}</TableCell>
                      <TableCell>{formatBytes(snap.size)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </SectionCard>
        </>
      )}
    </div>
  )
}
