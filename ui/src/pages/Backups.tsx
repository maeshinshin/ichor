import { useEffect, useState } from 'preact/hooks'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RestoreDialog } from '@/components/RestoreDialog'
import { BackupNow } from '@/components/BackupNow'
import type { EtcdBackup } from '@/gen/ichor/v1alpha1/operation_pb'
import { destinationLabel, exampleBackupYaml, newestSnapshot, totalBytes } from '@/lib/backups'
import { operationClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { formatBytes, formatTimestamp } from '@/lib/format'
import { useCluster } from '@/lib/useCluster'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }

const adrUrl = 'https://github.com/maeshinshin/ichor/blob/main/docs/adr/0047-etcd-backup-format-and-controller.md'

// Backups: the cluster's TalosEtcdBackups, the snapshots they hold, and restore (docs/adr/0047, 0089).
// A backup is created by applying a TalosEtcdBackup, and the controller takes the snapshots; Back up now only
// sets a request on it (docs/adr/0047).
export function Backups() {
  const t = useT()
  const { cluster, loading, error: clusterError } = useCluster()
  const [backups, setBackups] = useState<EtcdBackup[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const ref = cluster?.ref

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
        <PageHeader title={t('backups.title')} />
        <EmptyNote>{t('backups.noCluster')}</EmptyNote>
      </div>
    )
  }

  const all = backups.flatMap((b) => b.snapshots.map((s) => ({ backup: b, snap: s })))
  all.sort((a, b) => Number((b.snap.time?.seconds ?? 0n) - (a.snap.time?.seconds ?? 0n)) || a.snap.name.localeCompare(b.snap.name))

  return (
    <div class="space-y-8">
      <PageHeader
        title={t('backups.title')}
        kind={ref.name}
        actions={backups.length > 0 ? <RestoreDialog cluster={ref} disabled={all.length === 0} /> : undefined}
      />
      {backups.length === 0 ? (
        <section class="space-y-3">
          <h2 class="text-lg font-medium">{t('backups.emptyTitle')}</h2>
          <p class="text-sm text-muted-foreground">{t('backups.emptyBody')}</p>
          <pre class="overflow-x-auto rounded-md border bg-muted p-3 text-xs">{exampleBackupYaml(ref)}</pre>
          <a class="text-sm underline" href={adrUrl} target="_blank" rel="noreferrer">
            {t('backups.emptyDoc')}
          </a>
        </section>
      ) : (
        <>
          <section class="space-y-3">
            <h2 class="text-lg font-medium">{t('backups.configs')}</h2>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('backups.colName')}</TableHead>
                  <TableHead>{t('backups.colSchedule')}</TableHead>
                  <TableHead>{t('backups.colDestination')}</TableHead>
                  <TableHead>{t('backups.colRetention')}</TableHead>
                  <TableHead>{t('backups.colLast')}</TableHead>
                  <TableHead>{t('backups.colSnapshots')}</TableHead>
                  <TableHead>{t('backups.colSize')}</TableHead>
                  <TableHead>{t('backups.colStatus')}</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {backups.map((b) => {
                  const last = newestSnapshot(b.snapshots)
                  return (
                    <TableRow key={b.ref?.name}>
                      <TableCell class="font-medium">{b.ref?.name}</TableCell>
                      <TableCell class="font-mono text-xs">{b.schedule}</TableCell>
                      <TableCell class="font-mono text-xs">{destinationLabel(b.destination)}</TableCell>
                      <TableCell>{t('backups.keepLast', { count: b.keepLast })}</TableCell>
                      <TableCell>{b.lastSuccessTime ? formatTimestamp(b.lastSuccessTime) : last ? formatTimestamp(last.time) : t('backups.never')}</TableCell>
                      <TableCell>{b.snapshots.length}</TableCell>
                      <TableCell>{formatBytes(totalBytes(b.snapshots))}</TableCell>
                      <TableCell>
                        {b.suspended ? (
                          <StatusText tone="neutral">{t('backups.suspended')}</StatusText>
                        ) : (
                          <StatusText tone={b.ready ? 'good' : 'warn'}>{b.ready ? t('backups.ok') : t('backups.stale')}</StatusText>
                        )}
                      </TableCell>
                      <TableCell class="text-right">
                        <BackupNow backup={b} onChanged={(next) => setBackups(backups.map((x) => (x.ref?.name === next.ref?.name ? next : x)))} />
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
            <dl class="space-y-1 text-sm">
              {backups.map((b) => (
                <div key={b.ref?.name} class="flex flex-wrap gap-x-2">
                  <dt class="font-medium">{b.ref?.name}</dt>
                  <dd class="text-muted-foreground">
                    {b.manualBackupPending
                      ? t('backups.manualPending')
                      : b.lastManualBackup
                        ? t('backups.manualDone', { time: formatTimestamp(b.lastManualBackup) })
                        : t('backups.manualNone')}
                  </dd>
                  {b.lastError && (
                    <dd class="w-full break-words text-amber-600 dark:text-amber-400">
                      {t('backups.lastError')}: {b.lastError}
                    </dd>
                  )}
                </div>
              ))}
            </dl>
            <p class="text-sm text-muted-foreground">{t('backups.hint')}</p>
          </section>
          <section class="space-y-3">
            <h2 class="text-lg font-medium">{t('backups.snapshots')}</h2>
            {all.length === 0 ? (
              <EmptyNote>{t('backups.snapNone')}</EmptyNote>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('backups.snapTime')}</TableHead>
                    <TableHead>{t('backups.snapName')}</TableHead>
                    <TableHead>{t('backups.colName')}</TableHead>
                    <TableHead>{t('backups.colSize')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {all.map(({ backup, snap }) => (
                    <TableRow key={`${backup.ref?.name}/${snap.name}`}>
                      <TableCell>{formatTimestamp(snap.time)}</TableCell>
                      <TableCell class="font-mono text-xs">{snap.name}</TableCell>
                      <TableCell>{backup.ref?.name}</TableCell>
                      <TableCell>{formatBytes(snap.size)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
            <p class="text-sm text-muted-foreground">{t('backups.restoreHint')}</p>
          </section>
        </>
      )}
    </div>
  )
}
