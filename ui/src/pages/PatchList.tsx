import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import { ConfigLayer } from '@/gen/ichor/v1alpha1/node_pb'
import type { Patch } from '@/gen/ichor/v1alpha1/patch_pb'
import { clusterClient, patchClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { offersBreakGlass } from '@/lib/breakGlass'
import { describeError } from '@/lib/errors'
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
  if (!patches) return <p class="mt-10 text-center text-sm text-muted-foreground">{t('patches.loading')}</p>

  return (
    <Card className="mt-6">
      <CardHeader className="flex items-center justify-between">
        <CardTitle>{t('patches.title')}</CardTitle>
        <Button asChild size="sm">
          <Link to="/patches/new">{t('patches.new')}</Link>
        </Button>
      </CardHeader>
      <CardContent>
        <p class="mb-4 text-sm text-muted-foreground">{t('patches.intro')}</p>
        {patches.length === 0 ? (
          <p class="text-sm text-muted-foreground">{t('patches.empty')}</p>
        ) : (
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
              {patches.map((p) => (
                <TableRow key={`${p.ref?.namespace}/${p.ref?.name}`}>
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
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
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
        return
      }
      setOpen(false)
      onDeleted()
    } catch (err) {
      setError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="destructive">
          {t('patches.delete')}
        </Button>
      </DialogTrigger>
      <DialogContent>
        {pullRequestUrl ? (
          <>
            <PullRequestNotice url={pullRequestUrl} breakGlass={breakGlass} onBreakGlassDone={done} />
            <DialogFooter>
              <Button variant="ghost" onClick={() => setOpen(false)}>
                {t('common.close')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
        <DialogHeader>
          <DialogTitle>{t('patches.deleteTitle', { name: patch.ref?.name ?? '' })}</DialogTitle>
          <DialogDescription>{t('patches.deleteDescription')}</DialogDescription>
        </DialogHeader>
        {error && <ErrorAlert {...error} />}
        {refused && <BreakGlassDialog run={breakGlass} onDone={done} />}
        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant="destructive" disabled={submitting} onClick={submit}>
            {submitting ? t('patches.deleting') : t('patches.delete')}
          </Button>
        </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
