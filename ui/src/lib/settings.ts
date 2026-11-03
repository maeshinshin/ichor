// Pure helpers for the Settings page (docs/adr/0105, Part B).

// Bounds of the user lifetimes, in seconds; they mirror the CRD's rules and internal/settings.
export const TOKEN_TTL_BOUNDS = { min: 60, max: 3600 }
export const REFRESH_LIFETIME_BOUNDS = { min: 3600, max: 720 * 3600 }

export const TOKEN_TTL_PRESETS = ['1m', '5m', '15m', '30m', '1h']
export const REFRESH_LIFETIME_PRESETS = ['1h', '8h', '24h', '168h', '720h']

export const DRIFT_POLICIES = ['warnOnly', 'autoCorrect']

const UNIT_SECONDS: Record<string, number> = { h: 3600, m: 60, s: 1 }

// parseDuration reads a Go duration made of h, m and s parts ("15m0s", "1h", "720h"); null when it is anything else.
export function parseDuration(s: string): number | null {
  if (!/^(\d+[hms])+$/.test(s)) return null
  let total = 0
  for (const [, n, unit] of s.matchAll(/(\d+)([hms])/g)) total += Number(n) * UNIT_SECONDS[unit]
  return total
}

// formatDuration is the shortest spelling the server accepts: "15m0s" becomes "15m", 3600 becomes "1h".
export function formatDuration(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

// normalizeDuration shortens a server duration; anything it cannot read is returned as received.
export function normalizeDuration(s: string): string {
  const sec = parseDuration(s)
  return sec === null ? s : formatDuration(sec)
}

// durationOptions are the presets, plus the current value when it is not one of them (a flag default), in order.
export function durationOptions(presets: string[], current: string): string[] {
  const all = current && !presets.includes(current) ? [...presets, current] : presets
  return [...all].sort((a, b) => (parseDuration(a) ?? 0) - (parseDuration(b) ?? 0))
}

export type Bounds = { min: number; max: number }

// durationInBounds says a duration string is readable and inside the bounds.
export function durationInBounds(s: string, b: Bounds): boolean {
  const sec = parseDuration(s)
  return sec !== null && sec >= b.min && sec <= b.max
}

type Value = { value: string; source: string } | undefined
export type Fields = {
  tokenTtl?: string
  refreshLifetime?: string
  driftPolicy?: string
  autoPatchEnabled?: boolean
  autoPatchComponents?: string
  autoPatchWindow?: string
  autoPatchAcknowledgedHalt?: string
}
type Effective = { tokenTtl?: Value; refreshLifetime?: Value; driftPolicy?: Value; autoPatch?: Value; autoPatchComponents?: Value; autoPatchWindow?: Value }

export type SettingsView = {
  readOnly: boolean
  canEditSettings: boolean
  spec?: Fields
  effective?: Effective
  uiEffective?: Effective
  autoPatchHaltedOperation?: string
}

export type Form = { tokenTtl: string; refreshLifetime: string; driftPolicy: string }

// formOf is the value shown in each control: what the spec sets, else what is in force (the controller's report,
// else ichor-ui's own), else the default.
export function formOf(s: SettingsView): Form {
  const pick = (spec: string | undefined, key: 'tokenTtl' | 'refreshLifetime' | 'driftPolicy') =>
    spec ?? s.effective?.[key]?.value ?? s.uiEffective?.[key]?.value ?? ''
  return {
    tokenTtl: normalizeDuration(pick(s.spec?.tokenTtl, 'tokenTtl')),
    refreshLifetime: normalizeDuration(pick(s.spec?.refreshLifetime, 'refreshLifetime')),
    driftPolicy: pick(s.spec?.driftPolicy, 'driftPolicy') || DRIFT_POLICIES[0],
  }
}

export type Source = 'spec' | 'deployment'

// sourceOf says where a setting's value comes from: the spec when it sets it, else what the controller reported
// (or ichor-ui), else the deployment.
export function sourceOf(s: SettingsView, key: 'tokenTtl' | 'refreshLifetime' | 'driftPolicy'): Source {
  if (s.spec?.[key] !== undefined) return 'spec'
  const v = s.effective?.[key]?.source ?? s.uiEffective?.[key]?.source
  return v === 'spec' ? 'spec' : 'deployment'
}

// changesOf lists only the fields that differ from the form's starting point, in the shape UpdateSettings takes.
export function changesOf(before: Form, after: Form): Fields {
  const out: Fields = {}
  if (before.tokenTtl !== after.tokenTtl) out.tokenTtl = after.tokenTtl
  if (before.refreshLifetime !== after.refreshLifetime) out.refreshLifetime = after.refreshLifetime
  if (before.driftPolicy !== after.driftPolicy) out.driftPolicy = after.driftPolicy
  return out
}

// usersProblem is the i18n key of what is wrong with the lifetimes, or null.
export function usersProblem(f: Pick<Form, 'tokenTtl' | 'refreshLifetime'>): 'settings.badTokenTtl' | 'settings.badRefreshLifetime' | null {
  // The bounds already keep tokenTTL (at most 1h) below refreshLifetime (at least 1h).
  if (!durationInBounds(f.tokenTtl, TOKEN_TTL_BOUNDS)) return 'settings.badTokenTtl'
  if (!durationInBounds(f.refreshLifetime, REFRESH_LIFETIME_BOUNDS)) return 'settings.badRefreshLifetime'
  return null
}

// lockedReason is the i18n key of why the controls are disabled, or null when they are editable.
export function lockedReason(s: SettingsView): 'settings.readOnlyAlert' | 'settings.noPermissionAlert' | null {
  if (s.readOnly) return 'settings.readOnlyAlert'
  return s.canEditSettings ? null : 'settings.noPermissionAlert'
}

// The components automatic patching can apply, in the order it applies them (docs/adr/0116).
export const AUTO_PATCH_COMPONENTS = ['talos', 'cilium', 'kubernetes']

export type AutoPatchForm = { enabled: boolean; components: string[]; window: string }

// autoPatchFormOf is what the controls show: what the controller reports in force (status.effective), else what the
// spec sets, else off with every component and no window.
export function autoPatchFormOf(s: SettingsView): AutoPatchForm {
  const eff = s.effective
  const comps = eff?.autoPatchComponents?.value ?? s.spec?.autoPatchComponents ?? ''
  return {
    enabled: eff?.autoPatch ? eff.autoPatch.value === 'true' : (s.spec?.autoPatchEnabled ?? false),
    components: comps === '' ? [...AUTO_PATCH_COMPONENTS] : AUTO_PATCH_COMPONENTS.filter((c) => comps.split(',').includes(c)),
    window: eff?.autoPatchWindow?.value ?? s.spec?.autoPatchWindow ?? '',
  }
}

// autoPatchSource says where the switch's value comes from.
export function autoPatchSource(s: SettingsView): Source {
  if (s.spec?.autoPatchEnabled !== undefined) return 'spec'
  return s.effective?.autoPatch?.source === 'spec' ? 'spec' : 'deployment'
}

// toggleComponent adds or removes a component, keeping the canonical order.
export function toggleComponent(components: string[], name: string, on: boolean): string[] {
  return AUTO_PATCH_COMPONENTS.filter((c) => (c === name ? on : components.includes(c)))
}

const WINDOW = /^([01]\d|2[0-3]):[0-5]\d-([01]\d|2[0-3]):[0-5]\d$/

// autoPatchProblem is the i18n key of what is wrong with the form, or null. An empty window means any time; the
// window may wrap midnight; at least one component must be selected (none would be read as all).
export function autoPatchProblem(f: AutoPatchForm): 'settings.badWindow' | 'settings.noComponent' | null {
  if (f.window !== '' && (!WINDOW.test(f.window) || f.window.slice(0, 5) === f.window.slice(6))) return 'settings.badWindow'
  return f.components.length === 0 ? 'settings.noComponent' : null
}

// autoPatchChanges lists only what differs from the starting form; all components are sent as the empty list.
export function autoPatchChanges(before: AutoPatchForm, after: AutoPatchForm): Fields {
  const out: Fields = {}
  if (before.enabled !== after.enabled) out.autoPatchEnabled = after.enabled
  if (before.components.join(',') !== after.components.join(',')) {
    out.autoPatchComponents = after.components.length === AUTO_PATCH_COMPONENTS.length ? '' : after.components.join(',')
  }
  if (before.window !== after.window) out.autoPatchWindow = after.window
  return out
}
