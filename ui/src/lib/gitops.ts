// Pure helpers for the GitOps screen (docs/adr/0061). No import from "@/gen":
// these run directly under `node --test` (docs/adr/0045).
import { pullRequestHref } from './pullRequest.ts'

export type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline'

// externalBase returns a registered tool URL only if it may be linked: the same
// http(s)-only rule a pull request URL gets, with trailing slashes dropped so
// paths can be appended.
export function externalBase(url: string): string | null {
  const href = pullRequestHref(url)
  return href === null ? null : href.replace(/\/+$/, '')
}

// argoApplicationUrl is the Argo CD UI page of one Application. Argo CD's docs
// show /applications/<name> and say an Application in its own namespace may
// omit the namespace; an Application elsewhere is addressed <namespace>/<name>.
// The UI cannot tell which namespace is Argo CD's own, so "argocd" (the chart's
// default) is the only one written short.
export function argoApplicationUrl(base: string, app: { namespace: string; name: string }): string | null {
  const b = externalBase(base)
  if (b === null || !app.name) return null
  const name = encodeURIComponent(app.name)
  if (!app.namespace || app.namespace === 'argocd') return `${b}/applications/${name}`
  return `${b}/applications/${encodeURIComponent(app.namespace)}/${name}`
}

// The dashboard dist/chart/templates/grafana ships has a fixed uid; Grafana
// serves /d/<uid> without the slug. It has no cluster or node variable, so
// nothing is passed after it.
export const ichorDashboardUid = 'ichor-overview'

export function grafanaDashboardUrl(base: string): string | null {
  const b = externalBase(base)
  return b === null ? null : `${b}/d/${ichorDashboardUid}`
}

// syncBadge and healthBadge pick the colour of a state word. Anything not
// recognised is shown as it is, in the neutral style: a state Argo CD adds
// later must still be readable.
export function syncBadge(sync: string): BadgeVariant {
  if (sync === 'Synced') return 'secondary'
  if (sync === 'OutOfSync') return 'destructive'
  return 'outline'
}

export function healthBadge(health: string): BadgeVariant {
  if (health === 'Healthy') return 'secondary'
  if (health === 'Degraded' || health === 'Missing') return 'destructive'
  return 'outline'
}

// needsAttention is true for the rows worth reading first.
export function needsAttention(item: { error: string; syncStatus: string; healthStatus: string }): boolean {
  return item.error !== '' || item.syncStatus === 'OutOfSync' || item.healthStatus === 'Degraded' || item.healthStatus === 'Missing'
}

// trackingLabel is what the "Managed by" column says.
export function trackingLabel(trackedBy: string): string {
  return trackedBy === '' ? 'Not in Git' : trackedBy
}
