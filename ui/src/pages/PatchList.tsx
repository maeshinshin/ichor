import { useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { LayersIcon, PlusIcon } from 'lucide-preact'
import { LinkRow, ListToolbar } from '@/components/ListToolbar'
import { PageTitle } from '@/components/PageTitle'
import { SectionCard } from '@/components/SectionCard'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useAction } from '@/hooks/use-action'
import { ConfigLayer } from '@/gen/ichor/v1alpha1/node_pb'
import type { Patch } from '@/gen/ichor/v1alpha1/patch_pb'
import { patchClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { notifyDone } from '@/lib/notify'
import { matchesQuery } from '@/lib/filter'
import { targetKindOf } from '@/lib/patchTarget'
import { useCluster } from '@/lib/useCluster'
import { useList } from '@/lib/useList'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { t } from '@/lib/i18n'
import { Link } from '@/router'

// Patch list: every TalosConfigPatch the backend can see, across clusters
// (PatchService.ListPatches with no cluster filter, same "list everything,
// unconditionally" convention MachineList already uses for pending
// machines). Create/edit/delete all live here and on PatchDetail
// (architecture.md, "CLI and UI": "config editing by layer").
export function PatchList() {
  const { items: patches, error, reload } = useList(async () => (await patchClient.listPatches({})).patches)
  const [query, setQuery] = useState('')
  const { cluster } = useCluster()
  const perms = useMyPermissions(undefined, cluster?.ref ? { namespace: cluster.ref.namespace, name: cluster.ref.name } : undefined)

  if (error) return <ErrorAlert {...error} />
  if (!patches) return <PageSkeleton label={t('patches.loading')} />

  const rows = patches.filter((p) =>
    matchesQuery(query, p.ref?.name, enumLabel(ConfigLayer, p.layer), p.documents.map((d) => d.kind).join(' ')),
  )

  return (
    <div>
      <PageTitle title={t('config.title')} description={t('patches.intro')} />
      <ListToolbar query={query} onQuery={setQuery} shown={rows.length} total={patches.length} onRefresh={reload} stock>
        {perms?.createPatches && (
          <Button asChild size="sm">
            <Link to="/config/new">
              <PlusIcon data-icon="inline-start" aria-hidden="true" />
              {t('patches.new')}
            </Link>
          </Button>
        )}
      </ListToolbar>
      {patches.length === 0 ? (
        <EmptyNote icon={LayersIcon}>{t('patches.empty')}</EmptyNote>
      ) : rows.length === 0 ? (
        <EmptyNote icon={LayersIcon}>{t('list.noMatch')}</EmptyNote>
      ) : (
        <SectionCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('col.name')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('patches.colLayer')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('patches.colPriority')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('patches.colTarget')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('patches.colMachines')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('patches.colDocuments')}</TableHead>
                <TableHead>{t('col.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((p) => (
                <LinkRow key={`${p.ref?.namespace}/${p.ref?.name}`} to={p.ref ? `/config/${p.ref.name}` : '/config'}>
                  <TableCell className="max-md:whitespace-normal max-md:wrap-anywhere">
                    {p.ref ? (
                      <Button asChild variant="link" size="sm" className="max-md:h-auto max-md:whitespace-normal">
                        <Link to={`/config/${p.ref.name}`}>{p.ref.name}</Link>
                      </Button>
                    ) : (
                      '-'
                    )}
                    <div class="text-muted-foreground md:hidden">
                      {enumLabel(ConfigLayer, p.layer)} · {t('patches.colPriority')} {p.priority}
                    </div>
                    <div class="text-muted-foreground md:hidden">{t('patches.docCount', { count: p.documents.length })}</div>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <Badge variant="outline">{enumLabel(ConfigLayer, p.layer)}</Badge>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">{p.priority}</TableCell>
                  <TableCell className="hidden md:table-cell">{targetSummary(p)}</TableCell>
                  <TableCell className="hidden md:table-cell">{machinesSummary(p)}</TableCell>
                  <TableCell className="hidden md:table-cell">
                    {p.documents.length === 0 ? (
                      '-'
                    ) : (
                      <div class="flex flex-wrap items-center gap-1">
                        <span>{t('patches.docCount', { count: p.documents.length })}</span>
                        {p.documents.map((d, i) => (
                          <Badge key={i} variant="outline">
                            {d.kind}
                          </Badge>
                        ))}
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    <div class="flex gap-2 max-md:flex-wrap">
                      {perms?.updatePatches && (
                        <Button asChild size="sm" variant="outline">
                          <Link to={p.ref ? `/config/${p.ref.name}` : '/config'}>{t('patches.edit')}</Link>
                        </Button>
                      )}
                      {perms?.deletePatches && <DeleteDialog patch={p} onDeleted={reload} />}
                    </div>
                  </TableCell>
                </LinkRow>
              ))}
            </TableBody>
          </Table>
        </SectionCard>
      )}
    </div>
  )
}

// The patch's target as written, not the machines it resolves to: the
// controller owns that selection (docs/adr/0072).
function machinesSummary(p: Patch): string {
  if (p.layer === ConfigLayer.CLUSTER) return t('patches.machinesEvery')
  if (targetKindOf(p.target) === 'labelSelector') return t('patches.machinesSelector')
  return targetSummary(p)
}

function targetSummary(p: Patch): string {
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
  const [open, setOpen] = useState(false)
  const { busy: submitting, error, run } = useAction()

  async function submit() {
    const ref = patch.ref
    if (!ref) return
    await run(async () => {
      await patchClient.deletePatch({ patch: ref })
      setOpen(false)
      notifyDone(t('toast.patchDeleted', { name: ref.name }))
      onDeleted()
    })
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
      title={t('patches.deleteTitle', { name: patch.ref?.name ?? '' })}
      description={t('patches.deleteDescription')}
      confirm={{ label: submitting ? t('patches.deleting') : t('patches.delete'), disabled: submitting, onClick: submit }}
    >
      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
