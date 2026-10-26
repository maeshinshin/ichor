import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
import { DataTable, LinkRow, ListToolbar } from '@/components/ListToolbar'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import { ConfigLayer } from '@/gen/ichor/v1alpha1/node_pb'
import type { Patch } from '@/gen/ichor/v1alpha1/patch_pb'
import { clusterClient, patchClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { offersBreakGlass } from '@/lib/breakGlass'
import { notifyDone, notifyWrite } from '@/lib/notify'
import { describeError } from '@/lib/errors'
import { matchesQuery } from '@/lib/filter'
import { targetKindOf } from '@/lib/patchTarget'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

// Patch list: every TalosConfigPatch the backend can see, across clusters
// (PatchService.ListPatches with no cluster filter, same "list everything,
// unconditionally" convention MachineList already uses for pending
// machines). Create/edit/delete all live here and on PatchDetail
// (architecture.md, "CLI and UI": "config editing by layer").
export function PatchList() {
  const t = useT()
  const [patches, setPatches] = useState<Patch[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [clusterNames, setClusterNames] = useState<Record<string, string>>({})
  const [query, setQuery] = useState('')

  async function reload() {
    try {
      const [patchesRes, clustersRes] = await Promise.all([
        patchClient.listPatches({}),
        clusterClient.listClusters({}),
      ])
      setPatches(patchesRes.patches)
      setClusterNames(
        Object.fromEntries(
          clustersRes.clusters
            .filter((c) => c.ref)
            .map((c) => [`${c.ref!.namespace}/${c.ref!.name}`, c.ref!.name]),
        ),
      )
      setError(null)
    } catch (err) {
      setError(describeError(err))
    }
  }

  useEffect(() => {
    reload()
  }, [])

  if (error) return <ErrorAlert {...error} />
  if (!patches) return <PageSkeleton label={t('patches.loading')} />

  const clusterOf = (p: Patch) => (p.cluster ? clusterNames[`${p.cluster.namespace}/${p.cluster.name}`] ?? p.cluster.name : '')
  const rows = patches.filter((p) =>
    matchesQuery(query, p.ref?.name, clusterOf(p), enumLabel(ConfigLayer, p.layer), p.documents.map((d) => d.kind).join(' ')),
  )

  return (
    <div>
      <PageHeader title={t('patches.title')} />
      <p class="mb-4 max-w-3xl text-sm text-muted-foreground">{t('patches.intro')}</p>
      <ListToolbar query={query} onQuery={setQuery} shown={rows.length} total={patches.length} onRefresh={reload}>
        <Button asChild size="sm">
          <Link to="/patches/new">{t('patches.new')}</Link>
        </Button>
      </ListToolbar>
      {patches.length === 0 ? (
        <EmptyNote>{t('patches.empty')}</EmptyNote>
      ) : rows.length === 0 ? (
        <EmptyNote>{t('list.noMatch')}</EmptyNote>
      ) : (
        <DataTable>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('col.name')}</TableHead>
                <TableHead>{t('col.cluster')}</TableHead>
                <TableHead>{t('patches.colLayer')}</TableHead>
                <TableHead>{t('patches.colPriority')}</TableHead>
                <TableHead>{t('patches.colTarget')}</TableHead>
                <TableHead>{t('patches.colMachines')}</TableHead>
                <TableHead>{t('patches.colDocuments')}</TableHead>
                <TableHead>{t('col.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((p) => (
                <LinkRow key={`${p.ref?.namespace}/${p.ref?.name}`} to={objectRefPath('/patches', p.ref ?? { namespace: '', name: '' })}>
                  <TableCell>
                    {p.ref ? (
                      <Link to={objectRefPath('/patches', p.ref)} class="font-medium hover:underline">
                        {p.ref.name}
                      </Link>
                    ) : (
                      '-'
                    )}
                  </TableCell>
                  <TableCell>
                    {p.cluster ? clusterNames[`${p.cluster.namespace}/${p.cluster.name}`] ?? p.cluster.name : '-'}
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{enumLabel(ConfigLayer, p.layer)}</Badge>
                  </TableCell>
                  <TableCell>{p.priority}</TableCell>
                  <TableCell>{targetSummary(p, t)}</TableCell>
                  <TableCell>{machinesSummary(p, t)}</TableCell>
                  <TableCell>
                    {p.documents.length === 0 ? (
                      '-'
                    ) : (
                      <>
                        <div>{t('patches.docCount', { count: p.documents.length })}</div>
                        <div class="text-xs text-muted-foreground">{p.documents.map((d) => d.kind).join(', ')}</div>
                      </>
                    )}
                  </TableCell>
                  <TableCell>
                    <div class="flex gap-2">
                      <Button asChild size="sm" variant="outline">
                        <Link to={objectRefPath('/patches', p.ref ?? { namespace: '', name: '' })}>{t('patches.edit')}</Link>
                      </Button>
                      <DeleteDialog patch={p} onDeleted={reload} />
                    </div>
                  </TableCell>
                </LinkRow>
              ))}
            </TableBody>
          </Table>
        </DataTable>
      )}
    </div>
  )
}

// The patch's target as written, not the machines it resolves to: the
// controller owns that selection.
function machinesSummary(p: Patch, t: ReturnType<typeof useT>): string {
  if (p.layer === ConfigLayer.CLUSTER) return t('patches.machinesEvery')
  if (targetKindOf(p.target) === 'labelSelector') return t('patches.machinesSelector')
  return targetSummary(p, t)
}

function targetSummary(p: Patch, t: ReturnType<typeof useT>): string {
  if (p.layer === ConfigLayer.CLUSTER) return '-'
  switch (targetKindOf(p.target)) {
    case 'role':
      return t('patches.targetRole', { role: p.target!.role })
    case 'labelSelector':
      return t('patches.targetSelector')
    case 'machineRef':
      return t('patches.targetMachine', { name: p.target!.machineRef })
    default:
      return '-'
  }
}

function DeleteDialog({ patch, onDeleted }: { patch: Patch; onDeleted: () => void }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [pullRequestUrl, setPullRequestUrl] = useState<string | null>(null)
  const [refused, setRefused] = useState(false)

  // The same deletion again with break_glass set.
  const breakGlass: BreakGlassRun = async (bg) => {
    const res = await patchClient.deletePatch({ patch: patch.ref, breakGlass: bg })
    return { pullRequestUrl: res.pullRequestUrl, notice: res.notice }
  }
  const done = () => {
    setOpen(false)
    onDeleted()
  }

  async function submit() {
    if (!patch.ref) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await patchClient.deletePatch({ patch: patch.ref })
      if (res.pullRequestUrl) {
        // Tracked by Argo CD: the removal is a pull request, nothing was deleted.
        setPullRequestUrl(res.pullRequestUrl)
        notifyWrite(res, 'toast.patchDeleted', { name: patch.ref.name })
        return
      }
      setOpen(false)
      notifyDone(t('toast.patchDeleted', { name: patch.ref.name }))
      onDeleted()
    } catch (err) {
      setError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      trigger={
        <Button size="sm" variant="destructive">
          {t('patches.delete')}
        </Button>
      }
      title={pullRequestUrl ? t('pr.title') : t('patches.deleteTitle', { name: patch.ref?.name ?? '' })}
      description={pullRequestUrl ? undefined : t('patches.deleteDescription')}
      closeLabel={pullRequestUrl ? t('common.close') : undefined}
      confirm={pullRequestUrl ? undefined : { label: submitting ? t('patches.deleting') : t('patches.delete'), disabled: submitting, onClick: submit }}
    >
      {pullRequestUrl ? (
        <PullRequestNotice url={pullRequestUrl} breakGlass={breakGlass} onBreakGlassDone={done} />
      ) : (
        <>
          {error && <ErrorAlert {...error} />}
          {refused && <BreakGlassDialog run={breakGlass} onDone={done} />}
        </>
      )}
    </ConfirmDialog>
  )
}
