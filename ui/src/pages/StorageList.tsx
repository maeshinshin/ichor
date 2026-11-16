import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CodeBlock, CodeBlockContent, CodeBlockLine } from '@/components/ui/code-block'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { HardDriveIcon } from 'lucide-preact'
import { ConditionList } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { PageTitle } from '@/components/PageTitle'
import { SectionCard } from '@/components/SectionCard'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import type { Storage } from '@/gen/ichor/v1alpha1/storage_pb'
import { storageClient } from '@/lib/connect'
import { t } from '@/lib/i18n'
import { useCluster } from '@/lib/useCluster'
import { useList } from '@/lib/useList'
import { Link } from '@/router'

// Storage page: every TalosStorage of the cluster, with the nodes it governs and the disk preset it
// applies (StorageService.ListStorages). Read only: a TalosStorage is changed as a custom resource.
export function StorageList() {
  const { cluster } = useCluster()
  const { items, error } = useList(async () => (await storageClient.listStorages({ cluster: cluster?.ref })).storages)

  if (error) return <ErrorAlert {...error} />
  if (!items) return <PageSkeleton label={t('storage.loading')} />

  return (
    <div>
      <PageTitle title={t('storage.title')} description={t('storage.intro')} />
      {items.length === 0 ? (
        <EmptyNote icon={HardDriveIcon}>{t('storage.empty')}</EmptyNote>
      ) : (
        <div class="space-y-6">
          {items.map((s) => (
            <StorageCard key={s.ref?.name} storage={s} />
          ))}
        </div>
      )}
    </div>
  )
}

function StorageCard({ storage: s }: { storage: Storage }) {
  return (
    <SectionCard id={s.ref?.name} title={s.ref?.name} actions={<Badge variant="outline">{s.diskPreset}</Badge>}>
      {s.presetError && (
        <Alert variant="destructive">
          <AlertTitle>{t('storage.presetUnknown')}</AlertTitle>
          <AlertDescription>{s.presetError}</AlertDescription>
        </Alert>
      )}
      <Fields>
        <Field label={t('storage.nodes')} value={s.nodeSelector} />
        <Field label={t('storage.evacuation')} value={s.evacuationTimeout || t('storage.evacuationDefault')} />
        <Field label={t('storage.extensions')}>
          {s.requiredExtensions.length === 0 ? (
            '-'
          ) : (
            <div class="flex flex-wrap gap-1">
              {s.requiredExtensions.map((e) => (
                <Badge key={e} variant="outline">
                  {e}
                </Badge>
              ))}
            </div>
          )}
        </Field>
      </Fields>
      <Field label={t('storage.diskSelector')}>
        <CodeBlock size="sm">
          <CodeBlockContent wrap>
            <CodeBlockLine>{s.diskSelector}</CodeBlockLine>
          </CodeBlockContent>
        </CodeBlock>
      </Field>
      <h3 class="text-sm font-medium">{t('storage.governs')}</h3>
      {s.governedMachines.length === 0 ? (
        <EmptyNote>{t('storage.governsNone')}</EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('col.name')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {s.governedMachines.map((m) => (
              <TableRow key={m.name}>
                <TableCell>
                  <Button asChild variant="link" size="sm">
                    <Link to={`/nodes/${m.name}`}>{m.name}</Link>
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <h3 class="text-sm font-medium">{t('storage.conditions')}</h3>
      <ConditionList conditions={s.conditions} />
    </SectionCard>
  )
}
