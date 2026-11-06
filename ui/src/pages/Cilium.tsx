import { useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CircleXIcon, InfoIcon, NetworkIcon, TriangleAlertIcon } from 'lucide-preact'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { CodeBlock, CodeBlockContent, CodeBlockLine } from '@/components/ui/code-block'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageTitle } from '@/components/PageTitle'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { CiliumUpgradeDialog } from '@/components/CiliumUpgradeDialog'
import type { GetCiliumValuesResponse } from '@/gen/ichor/v1alpha1/cni_pb'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { canApply, diffLines, type DiffKind } from '@/lib/cilium'
import { cniClient } from '@/lib/connect'
import { detectedCni, isCniUnmanaged } from '@/lib/cni'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import { useCluster } from '@/lib/useCluster'
import { useLoad } from '@/lib/useLoad'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { t } from '@/lib/i18n'

type Reviewed = { text: string; valid: boolean; errors: string[]; diff: string }

// The code block's documented line variants stand for the kinds of a diff line.
const diffVariant: Record<DiffKind, 'added' | 'removed' | 'highlighted' | 'default'> = {
  add: 'added',
  del: 'removed',
  hunk: 'highlighted',
  meta: 'highlighted',
  ctx: 'default',
}

// Cilium: the Cilium Helm values of the cluster (docs/adr/0099). Three layers meet here: Ichor's
// defaults, the user layer edited on this page, and the keys Ichor owns. A write goes through
// SetCiliumValues, which sets spec.cni.cilium.values; the controller applies it.
export function Cilium() {
  const { cluster, loading, error: clusterError } = useCluster()
  const ref = cluster?.ref
  const unmanaged = !!cluster && isCniUnmanaged(cluster.cniPreset)
  const [version, setVersion] = useState(0)
  // An unmanaged cluster has no values to read; the server would refuse the call.
  const { data, error } = useLoad(ref && !unmanaged ? () => cniClient.getCiliumValues({ cluster: ref }) : undefined, [ref?.namespace, ref?.name, version])
  const perms = useMyPermissions(undefined, ref)

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (cluster && unmanaged) {
    const detected = [detectedCni(cluster.cniDetected), cluster.cniDetectedVersion].filter((s) => s !== '').join(' ')
    return (
      <div class="space-y-4">
        <PageTitle title={t('cilium.title')} description={cluster.ref?.name} />
        <Card>
          <CardHeader>
            <CardTitle>{t('cni.unmanagedTitle')}</CardTitle>
            <CardDescription>{t('cni.unmanagedDetected', { detected })}</CardDescription>
          </CardHeader>
          <CardContent>{t('cni.unmanagedNote')}</CardContent>
        </Card>
      </div>
    )
  }
  if (error) return <ErrorAlert {...error} />
  if (loading || (ref && !data)) return <PageSkeleton label={t('cilium.loading')} />
  if (!ref || !data) {
    return (
      <div>
        <PageTitle title={t('cilium.title')} />
        <EmptyNote icon={NetworkIcon}>{t('cilium.noCluster')}</EmptyNote>
      </div>
    )
  }

  const editable = !!perms?.clusterUpdate

  return (
    <div class="space-y-4">
      <PageTitle title={t('cilium.title')} description={ref.name} />
      {perms && !perms.clusterUpdate && (
        <Alert>
          <InfoIcon />
          <AlertDescription>{t('cilium.noWrite')}</AlertDescription>
        </Alert>
      )}
      {perms?.clusterUpdate && !data.canEdit && (
        <Alert>
          <InfoIcon />
          <AlertDescription>{t('cilium.noValuesWrite')}</AlertDescription>
        </Alert>
      )}
      <Card id="effective" className="scroll-mt-section">
        <CardHeader>
          <CardTitle>{t('cilium.effective')}</CardTitle>
          <CardDescription>{t('cilium.effectiveHint')}</CardDescription>
        </CardHeader>
        <CardContent>
          <CodeBlock>
            <CodeBlockContent className="max-h-96">{data.effectiveYaml}</CodeBlockContent>
          </CodeBlock>
        </CardContent>
      </Card>
      <OwnedKeys owned={data.owned} />
      <UserValues key={version} cluster={ref} data={data} editable={editable && data.canEdit} onApplied={() => setVersion(version + 1)} />
      <CiliumUpgrade cluster={ref} chart={data.availableChartVersion} editable={editable} />
    </div>
  )
}

function OwnedKeys({ owned }: { owned: GetCiliumValuesResponse['owned'] }) {
  return (
    <Card id="owned" className="scroll-mt-section">
      <CardHeader>
        <CardTitle>{t('cilium.owned')}</CardTitle>
        <CardDescription>{t('cilium.ownedHint')}</CardDescription>
      </CardHeader>
      <CardContent>
      {owned.length === 0 ? (
        <EmptyNote icon={NetworkIcon}>{t('cilium.ownedNone')}</EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('cilium.colKey')}</TableHead>
              <TableHead className="max-md:hidden">{t('cilium.colValue')}</TableHead>
              <TableHead className="max-md:hidden">{t('cilium.colReason')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {owned.map((k) => (
              <TableRow key={k.path}>
                <TableCell className="align-top max-md:whitespace-normal max-md:wrap-anywhere">
                  <div class="flex flex-col items-start gap-1">
                    {k.path}
                    <Badge variant="secondary">{t('cilium.managedBadge')}</Badge>
                  </div>
                  <div class="mt-2 space-y-2 md:hidden">
                    <CodeBlock size="sm">
                      <CodeBlockContent wrap>{k.valueYaml.trimEnd()}</CodeBlockContent>
                    </CodeBlock>
                    <div class="text-muted-foreground">{k.reason}</div>
                  </div>
                </TableCell>
                <TableCell className="align-top max-md:hidden">
                  <CodeBlock size="sm">
                    <CodeBlockContent wrap>{k.valueYaml.trimEnd()}</CodeBlockContent>
                  </CodeBlock>
                </TableCell>
                <TableCell className="align-top whitespace-normal max-md:hidden">{k.reason}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      </CardContent>
    </Card>
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
    <Card id="user" className="scroll-mt-section">
      <CardHeader>
        <CardTitle>{t('cilium.user')}</CardTitle>
        <CardDescription>{t('cilium.userHint')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
      {!editable ? (
        <CodeBlock>
          <CodeBlockContent className="max-h-96">{data.userYaml || t('cilium.userEmpty')}</CodeBlockContent>
        </CodeBlock>
      ) : (
        <>
          <Textarea
            aria-label={t('cilium.userLabel')}
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
      {stale && (
        <Alert>
          <TriangleAlertIcon />
          <AlertDescription>{t('cilium.stale')}</AlertDescription>
        </Alert>
      )}
      {reviewed && !stale && !reviewed.valid && (
        <Alert variant="destructive">
          <CircleXIcon />
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
      {reviewed && !stale && reviewed.valid && reviewed.diff === '' && (
        <Alert>
          <InfoIcon />
          <AlertDescription>{t('cilium.diffNone')}</AlertDescription>
        </Alert>
      )}
      {lines.length > 0 && (
        <Card size="sm">
          <CardHeader>
            <CardTitle>{t('cilium.diff')}</CardTitle>
          </CardHeader>
          <CardContent>
            <CodeBlock>
              <CodeBlockContent className="max-h-96">
                {lines.map((l, i) => (
                  <CodeBlockLine key={i} variant={diffVariant[l.kind]}>
                    {l.text}
                  </CodeBlockLine>
                ))}
              </CodeBlockContent>
            </CodeBlock>
          </CardContent>
        </Card>
      )}
      </CardContent>
    </Card>
  )
}

// CiliumUpgrade shows the upgrade to the chart this Ichor embeds when it is newer than the release. Nothing is
// shown to a viewer who may not upgrade, or when the cluster is up to date: a newer Cilium arrives with a newer
// Ichor (docs/adr/0115, amendment 2026-11-04 (3)).
function CiliumUpgrade({ cluster, chart, editable }: { cluster: ObjectRef; chart: string; editable: boolean }) {
  if (!editable || chart === '') return null

  return (
    <Card id="upgrade" className="scroll-mt-section">
      <CardHeader>
        <CardTitle>{t('cilium.upgradeTitle')}</CardTitle>
        <CardDescription>{t('cilium.upgradeAvailable', { version: chart })}</CardDescription>
      </CardHeader>
      <CardFooter>
        <CiliumUpgradeDialog cluster={cluster} chart={chart} />
      </CardFooter>
    </Card>
  )
}
