import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section } from '@/components/Section'
import type { GetCiliumValuesResponse } from '@/gen/ichor/v1alpha1/cni_pb'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { canApply, canEdit, canUpgrade, diffLines, type DiffKind } from '@/lib/cilium'
import { cniClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import { useCluster } from '@/lib/useCluster'
import { useLoad } from '@/lib/useLoad'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }
type Reviewed = { text: string; valid: boolean; errors: string[]; diff: string }

const diffClass: Record<DiffKind, string> = {
  add: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300',
  del: 'bg-red-500/15 text-red-700 dark:text-red-300',
  hunk: 'text-sky-700 dark:text-sky-300',
  meta: 'font-semibold text-muted-foreground',
  ctx: '',
}

// Cilium: the Cilium Helm values of the cluster. Three layers meet here: Ichor's
// defaults, the user layer edited on this page, and the keys Ichor owns. A write goes through
// SetCiliumValues, which sets spec.cni.cilium.values; the controller applies it.
export function Cilium() {
  const t = useT()
  const { cluster, loading, error: clusterError } = useCluster()
  const ref = cluster?.ref
  const [version, setVersion] = useState(0)
  const { data, error } = useLoad(ref ? () => cniClient.getCiliumValues({ cluster: ref }) : undefined, [ref?.namespace, ref?.name, version])
  const perms = useMyPermissions(undefined, ref)

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (error) return <ErrorAlert {...error} />
  if (loading || (ref && !data)) return <PageSkeleton label={t('cilium.loading')} />
  if (!ref || !data) {
    return (
      <div>
        <PageHeader title={t('cilium.title')} />
        <EmptyNote>{t('cilium.noCluster')}</EmptyNote>
      </div>
    )
  }

  const tracked = data.trackedBy !== ''
  const editable = canEdit(perms, data.trackedBy)

  return (
    <div class="space-y-8">
      <PageHeader title={t('cilium.title')} kind={ref.name} badges={tracked ? <Badge variant="outline">{t('cilium.tracked', { tool: data.trackedBy })}</Badge> : undefined} />
      {tracked && (
        <Alert>
          <AlertTitle>{t('cilium.tracked', { tool: data.trackedBy })}</AlertTitle>
          <AlertDescription>{t('cilium.trackedBody', { tool: data.trackedBy })}</AlertDescription>
        </Alert>
      )}
      {!tracked && perms && !perms.clusterUpdate && <p class="text-sm text-muted-foreground">{t('cilium.noWrite')}</p>}
      <Section id="effective" title={t('cilium.effective')}>
        <p class="text-muted-foreground">{t('cilium.effectiveHint')}</p>
        <pre class="max-h-96 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs">{data.effectiveYaml}</pre>
      </Section>
      <OwnedKeys owned={data.owned} />
      <UserValues key={version} cluster={ref} data={data} editable={editable} onApplied={() => setVersion(version + 1)} />
      {canUpgrade(data.availableChartVersion, editable) && <Upgrade cluster={ref} version={data.availableChartVersion} />}
    </div>
  )
}

function OwnedKeys({ owned }: { owned: GetCiliumValuesResponse['owned'] }) {
  const t = useT()
  return (
    <Section id="owned" title={t('cilium.owned')}>
      <p class="text-muted-foreground">{t('cilium.ownedHint')}</p>
      {owned.length === 0 ? (
        <EmptyNote>{t('cilium.ownedNone')}</EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('cilium.colKey')}</TableHead>
              <TableHead>{t('cilium.colValue')}</TableHead>
              <TableHead>{t('cilium.colReason')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {owned.map((k) => (
              <TableRow key={k.path}>
                <TableCell class="align-top">
                  <div class="font-mono text-xs">{k.path}</div>
                  <Badge variant="secondary" className="mt-1">
                    {t('cilium.managedBadge')}
                  </Badge>
                </TableCell>
                <TableCell class="align-top font-mono text-xs whitespace-pre-wrap">{k.valueYaml.trimEnd()}</TableCell>
                <TableCell class="align-top whitespace-normal">{k.reason}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Section>
  )
}

function UserValues({
  cluster,
  data,
  editable,
  onApplied,
}: {
  cluster: ObjectRef
  data: GetCiliumValuesResponse
  editable: boolean
  onApplied: () => void
}) {
  const t = useT()
  const [text, setText] = useState(data.userYaml)
  const [reviewed, setReviewed] = useState<Reviewed | null>(null)
  const [busy, setBusy] = useState<'validate' | 'preview' | 'apply' | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [confirm, setConfirm] = useState(false)

  async function check(withDiff: boolean) {
    setBusy(withDiff ? 'preview' : 'validate')
    setError(null)
    try {
      const v = await cniClient.validateCiliumValues({ cluster, userYaml: text })
      const diff = withDiff && v.valid ? (await cniClient.diffCiliumValues({ cluster, userYaml: text })).diff : ''
      setReviewed({ text, valid: v.valid, errors: v.errors, diff })
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(null)
    }
  }

  async function apply() {
    setBusy('apply')
    setError(null)
    try {
      await cniClient.setCiliumValues({ cluster, userYaml: text })
      setConfirm(false)
      notifyDone(t('cilium.applied'))
      onApplied()
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(null)
    }
  }

  const stale = reviewed !== null && reviewed.text !== text
  const lines = reviewed && !stale ? diffLines(reviewed.diff) : []

  return (
    <Section id="user" title={t('cilium.user')}>
      <p class="text-muted-foreground">{t('cilium.userHint')}</p>
      {!editable ? (
        <pre class="max-h-96 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs">{data.userYaml || t('cilium.userEmpty')}</pre>
      ) : (
        <>
          <Textarea
            aria-label={t('cilium.userLabel')}
            className="font-mono text-xs"
            rows={14}
            spellcheck={false}
            value={text}
            onInput={(e) => setText((e.target as HTMLTextAreaElement).value)}
          />
          <div class="flex flex-wrap gap-2">
            <Button variant="outline" disabled={busy !== null} onClick={() => check(false)}>
              {busy === 'validate' ? t('cilium.validating') : t('cilium.validate')}
            </Button>
            <Button variant="outline" disabled={busy !== null} onClick={() => check(true)}>
              {busy === 'preview' ? t('cilium.previewing') : t('cilium.preview')}
            </Button>
            <ConfirmDialog
              open={confirm}
              onOpenChange={(next) => {
                setConfirm(next)
                if (!next) setError(null)
              }}
              trigger={<Button disabled={busy !== null || !canApply(text, reviewed)}>{t('cilium.apply')}</Button>}
              title={t('cilium.confirmTitle')}
              description={t('cilium.confirmBody')}
              confirm={{ label: busy === 'apply' ? t('cilium.applying') : t('cilium.apply'), disabled: busy !== null, onClick: apply, destructive: false }}
            >
              {error && <ErrorAlert {...error} />}
            </ConfirmDialog>
            <Button variant="ghost" disabled={busy !== null || text === data.userYaml} onClick={() => { setText(data.userYaml); setReviewed(null) }}>
              {t('cilium.revert')}
            </Button>
          </div>
        </>
      )}
      {!confirm && error && <ErrorAlert {...error} />}
      {stale && <p class="text-sm text-amber-600 dark:text-amber-400">{t('cilium.stale')}</p>}
      {reviewed && !stale && !reviewed.valid && (
        <Alert variant="destructive">
          <AlertTitle>{reviewed.errors.length === 1 ? t('cilium.invalidOne') : t('cilium.invalid', { count: reviewed.errors.length })}</AlertTitle>
          <AlertDescription>
            <ul class="list-disc space-y-1 pl-5">
              {reviewed.errors.map((e, i) => (
                <li key={i} class="break-words">
                  {e}
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {reviewed && !stale && reviewed.valid && reviewed.diff === '' && <p class="text-sm text-muted-foreground">{t('cilium.diffNone')}</p>}
      {lines.length > 0 && (
        <div class="space-y-2">
          <h3 class="text-sm font-medium">{t('cilium.diff')}</h3>
          <pre class="max-h-96 overflow-auto rounded-md border font-mono text-xs">
            {lines.map((l, i) => (
              <div key={i} class={`px-3 whitespace-pre ${diffClass[l.kind]}`}>
                {l.text || ' '}
              </div>
            ))}
          </pre>
        </div>
      )}
    </Section>
  )
}

// Upgrade starts the Cilium chart upgrade. The server can still answer Unimplemented, and then the error text is shown as received.
function Upgrade({ cluster, version }: { cluster: ObjectRef; version: string }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => setError(null), [version])

  async function start() {
    setBusy(true)
    setError(null)
    try {
      await cniClient.startCniUpgrade({ cluster })
      setOpen(false)
      notifyDone(t('cilium.upgradeStarted'))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section id="upgrade" title={t('cilium.upgradeTitle')}>
      <p>{t('cilium.upgradeAvailable', { version })}</p>
      <ConfirmDialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) setError(null)
        }}
        trigger={<Button variant="outline">{t('cilium.upgradeStart')}</Button>}
        title={t('cilium.upgradeConfirmTitle', { version })}
        description={t('cilium.upgradeConfirmBody')}
        confirm={{ label: busy ? t('cilium.upgradeStarting') : t('cilium.upgrade'), disabled: busy, onClick: start, destructive: false }}
      >
        {error && <ErrorAlert {...error} />}
      </ConfirmDialog>
    </Section>
  )
}
