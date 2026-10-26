import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ErrorAlert } from '@/components/ErrorAlert'
import { GitOpsKind, type FindPullRequestsResponse, type GetLinksResponse, type GitOpsItem } from '@/gen/ichor/v1alpha1/gitops_pb'
import { gitOpsClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import {
  argoApplicationUrl,
  externalBase,
  grafanaDashboardUrl,
  healthBadge,
  needsAttention,
  syncBadge,
  trackingLabel,
} from '@/lib/gitops'
import { pullRequestHref } from '@/lib/pullRequest'
import { useT } from '@/lib/useT'
import type { Key } from '@/lib/i18n'

type ErrorShape = { title: string; detail: string }

const kindLabel: Record<number, Key> = {
  [GitOpsKind.CLUSTER]: 'gitops.kindCluster',
  [GitOpsKind.MACHINE]: 'gitops.kindMachine',
  [GitOpsKind.PATCH]: 'gitops.kindPatch',
}

const rowKey = (i: GitOpsItem) => `${i.kind}/${i.ref?.namespace}/${i.ref?.name}`

function ExternalLink({ href, children }: { href: string | null; children: string }) {
  if (href === null) return null
  return (
    <a class="underline" href={href} target="_blank" rel="noreferrer">
      {children}
    </a>
  )
}

// GitOps: each CR's Argo CD Application with its sync and health state, the
// pull requests still open for it, and links into the tools that are registered. Opening the page reads only the cluster; the Git host is
// asked only when a row's "Pull requests" button is pressed.
export function GitOps() {
  const t = useT()
  const [items, setItems] = useState<GitOpsItem[] | null>(null)
  const [links, setLinks] = useState<GetLinksResponse | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [prs, setPrs] = useState<Record<string, FindPullRequestsResponse | ErrorShape | 'loading'>>({})

  useEffect(() => {
    let cancelled = false
    gitOpsClient.listGitOps({}).then(
      (res) => !cancelled && setItems(res.items),
      (err) => !cancelled && setError(describeError(err)),
    )
    // Links are a convenience: failing to read them must not hide the table.
    gitOpsClient.getLinks({}).then(
      (res) => !cancelled && setLinks(res),
      () => undefined,
    )
    return () => {
      cancelled = true
    }
  }, [])

  async function checkPullRequests(item: GitOpsItem) {
    const key = rowKey(item)
    setPrs((p) => ({ ...p, [key]: 'loading' }))
    try {
      const res = await gitOpsClient.findPullRequests({ kind: item.kind, ref: item.ref })
      setPrs((p) => ({ ...p, [key]: res }))
    } catch (err) {
      setPrs((p) => ({ ...p, [key]: describeError(err) }))
    }
  }

  if (error) return <ErrorAlert {...error} />
  if (!items) return <PageSkeleton label={t('gitops.loading')} />

  const sorted = [...items].sort((a, b) => Number(needsAttention(b)) - Number(needsAttention(a)))

  const toolLinks = links
    ? [
        { label: t('gitops.grafana'), href: grafanaDashboardUrl(links.grafana) },
        { label: 'Hubble', href: externalBase(links.hubble) },
        { label: 'Longhorn', href: externalBase(links.longhorn) },
        { label: 'Argo CD', href: externalBase(links.argocd) },
      ].filter((l) => l.href !== null)
    : []

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('gitops.title')}</CardTitle>
        {toolLinks.length > 0 && (
          <p class="flex flex-wrap gap-4 text-sm">
            {toolLinks.map((l) => (
              <ExternalLink key={l.label} href={l.href}>
                {l.label}
              </ExternalLink>
            ))}
          </p>
        )}
      </CardHeader>
      <CardContent>
        {sorted.length === 0 ? (
          <EmptyNote>{t('gitops.empty')}</EmptyNote>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('gitops.colResource')}</TableHead>
                <TableHead>{t('col.cluster')}</TableHead>
                <TableHead>{t('gitops.colManagedBy')}</TableHead>
                <TableHead>{t('gitops.colApplication')}</TableHead>
                <TableHead>{t('gitops.colSync')}</TableHead>
                <TableHead>{t('gitops.colHealth')}</TableHead>
                <TableHead>{t('gitops.colPullRequests')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.map((item) => (
                <GitOpsRow
                  key={rowKey(item)}
                  item={item}
                  argocd={links?.argocd ?? ''}
                  pr={prs[rowKey(item)]}
                  onCheck={() => checkPullRequests(item)}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function GitOpsRow(props: {
  item: GitOpsItem
  argocd: string
  pr: FindPullRequestsResponse | ErrorShape | 'loading' | undefined
  onCheck: () => void
}) {
  const { item, pr } = props
  const t = useT()
  const appHref = item.application ? argoApplicationUrl(props.argocd, item.application) : null
  const canHavePr = item.kind === GitOpsKind.MACHINE || item.kind === GitOpsKind.PATCH

  return (
    <TableRow>
      <TableCell>
        <span class="text-muted-foreground">{kindLabel[item.kind] ? t(kindLabel[item.kind]) : '-'} </span>
        <span class="font-medium">{item.ref?.name}</span>
      </TableCell>
      <TableCell>{item.cluster || '-'}</TableCell>
      <TableCell>{trackingLabel(item.trackedBy, t('gitops.notInGit'))}</TableCell>
      <TableCell>
        {item.application ? (
          appHref ? (
            <ExternalLink href={appHref}>{item.application.name}</ExternalLink>
          ) : (
            item.application.name
          )
        ) : (
          '-'
        )}
        {item.error && <p class="text-xs text-destructive">{item.error}</p>}
      </TableCell>
      {item.permissionDenied ? (
        <TableCell colSpan={2} class="text-muted-foreground">
          {t('common.noPermission')}
        </TableCell>
      ) : (
        <>
          <TableCell>{item.syncStatus ? <Badge variant={syncBadge(item.syncStatus)}>{item.syncStatus}</Badge> : '-'}</TableCell>
          <TableCell>
            {item.healthStatus ? <Badge variant={healthBadge(item.healthStatus)}>{item.healthStatus}</Badge> : '-'}
          </TableCell>
        </>
      )}
      <TableCell>
        {canHavePr && pr === undefined && (
          <Button variant="outline" size="sm" onClick={props.onCheck}>
            {t('gitops.check')}
          </Button>
        )}
        {pr === 'loading' && <span class="text-muted-foreground">{t('gitops.checking')}</span>}
        {pr && pr !== 'loading' && 'title' in pr && <span class="text-xs text-destructive">{pr.title}: {pr.detail}</span>}
        {pr && pr !== 'loading' && !('title' in pr) && <PullRequestList res={pr} />}
      </TableCell>
    </TableRow>
  )
}

function PullRequestList({ res }: { res: FindPullRequestsResponse }) {
  const t = useT()
  if (res.note) return <span class="text-xs text-muted-foreground">{res.note}</span>
  if (res.pullRequests.length === 0) return <span class="text-muted-foreground">{t('gitops.noneOpen')}</span>
  return (
    <ul class="text-sm">
      {res.pullRequests.map((p) => {
        const href = pullRequestHref(p.url)
        return (
          <li key={p.url}>
            {p.action}: {href ? <ExternalLink href={href}>{p.url}</ExternalLink> : p.url}
          </li>
        )
      })}
    </ul>
  )
}
