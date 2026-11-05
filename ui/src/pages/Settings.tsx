import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useAction } from '@/hooks/use-action'
import { PageSkeleton } from '@/components/PageStates'
import { PageTitle } from '@/components/PageTitle'
import { SectionCard } from '@/components/SectionCard'
import type { GetSettingsResponse } from '@/gen/ichor/v1alpha1/settings_pb'
import { operationClient, settingsClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyWrite } from '@/lib/notify'
import {
  AUTO_PATCH_COMPONENTS,
  autoPatchChanges,
  autoPatchFormOf,
  autoPatchProblem,
  autoPatchSource,
  toggleComponent,
  changesOf,
  DRIFT_POLICIES,
  type Fields,
  durationOptions,
  formOf,
  lockedReason,
  normalizeDuration,
  REFRESH_LIFETIME_PRESETS,
  sourceOf,
  TOKEN_TTL_PRESETS,
  usersProblem,
  type AutoPatchForm,
  type Form,
  type Source,
} from '@/lib/settings'
import { useLoad } from '@/lib/useLoad'
import { Switch } from '@/components/ui/switch'
import { t } from '@/lib/i18n'

// The Settings page: the runtime settings of the IchorInstallation, one card per
// group. Each value carries a badge for where it comes from. Writes go through SettingsService as the
// caller; a read-only deployment or a missing verb leaves the controls disabled with the reason in an Alert.
export function Settings() {
  const [res, setRes] = useState<GetSettingsResponse | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => {
    settingsClient.getSettings({}).then(setRes, (err) => setError(describeError(err)))
  }, [])

  if (error && !res) return <ErrorAlert {...error} />
  if (!res) return <PageSkeleton label={t('settings.loading')} />

  return (
    <div className="space-y-4">
      <PageTitle title={t('settings.title')} />
      <DeploymentCard res={res} />
      <UsersCard res={res} onSaved={setRes} />
      <DefaultsCard res={res} onSaved={setRes} />
      <UpdatesCard res={res} onSaved={setRes} />
      <AutoPatchCard res={res} onSaved={setRes} />
    </div>
  )
}

function SourceBadge({ source }: { source: Source }) {
  return <Badge variant={source === 'spec' ? 'secondary' : 'outline'}>{t(source === 'spec' ? 'settings.sourceSpec' : 'settings.sourceDeployment')}</Badge>
}

function Locked({ res }: { res: GetSettingsResponse }) {
  const reason = lockedReason(res)
  if (!reason) return null
  return (
    <Alert>
      <AlertDescription>{t(reason)}</AlertDescription>
    </Alert>
  )
}

function DeploymentCard({ res }: { res: GetSettingsResponse }) {
  const helm = res.manager === 'helm'
  return (
    <SectionCard title={t('settings.deployment')} description={t('settings.deploymentDesc')}>
      {helm && (
        <Alert>
          <AlertDescription>{t('settings.helmNotice')}</AlertDescription>
        </Alert>
      )}
      <FieldGroup>
        <Fact label={t('settings.version')} value={res.version} />
        <Fact label={t('settings.installedVersion')} value={res.installedVersion || '-'} />
        <Fact label={t('settings.manager')} value={t(helm ? 'settings.managerHelm' : 'settings.managerInstaller')} />
      </FieldGroup>
    </SectionCard>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <Field orientation="horizontal">
      <FieldLabel>{label}</FieldLabel>
      <span className="text-sm">{value}</span>
    </Field>
  )
}

// useSave sends one UpdateSettings call and reports the answer; the card shows its own error.
function useSave(onSaved: (r: GetSettingsResponse) => void) {
  const { busy, error, run } = useAction()
  async function save(changes: Fields & { updateCheckEnabled?: boolean }): Promise<boolean> {
    let ok = false
    await run(async () => {
      const out = await settingsClient.updateSettings({ changes })
      if (out.settings) onSaved(out.settings)
      notifyWrite('settings.saved')
      ok = true
    })
    return ok
  }
  return { busy, error, save }
}

function UsersCard({ res, onSaved }: { res: GetSettingsResponse; onSaved: (r: GetSettingsResponse) => void }) {
  const start = formOf(res)
  const [form, setForm] = useState<Form>(start)
  const { busy, error, save } = useSave(onSaved)
  const locked = lockedReason(res) !== null
  const changes = changesOf(start, form)
  const problem = usersProblem(form)
  const ui = res.uiEffective

  return (
    <SectionCard title={t('settings.users')} description={t('settings.usersDesc')}>
      <Locked res={res} />
      <FieldGroup>
        <DurationField
          id="settings-token-ttl"
          label={t('settings.tokenTtl')}
          value={form.tokenTtl}
          options={durationOptions(TOKEN_TTL_PRESETS, form.tokenTtl)}
          source={sourceOf(res, 'tokenTtl')}
          disabled={locked || busy}
          onChange={(v) => setForm({ ...form, tokenTtl: v })}
        />
        <DurationField
          id="settings-refresh-lifetime"
          label={t('settings.refreshLifetime')}
          value={form.refreshLifetime}
          options={durationOptions(REFRESH_LIFETIME_PRESETS, form.refreshLifetime)}
          source={sourceOf(res, 'refreshLifetime')}
          disabled={locked || busy}
          onChange={(v) => setForm({ ...form, refreshLifetime: v })}
        />
      </FieldGroup>
      {ui?.tokenTtl && ui.refreshLifetime && (
        <p className="text-sm text-muted-foreground">
          {t('settings.uiApplies', { ttl: normalizeDuration(ui.tokenTtl.value), refresh: normalizeDuration(ui.refreshLifetime.value) })}
        </p>
      )}
      {problem && <p className="text-sm text-destructive">{t(problem)}</p>}
      {error && <ErrorAlert {...error} />}
      <Button disabled={locked || busy || problem !== null || Object.keys(changes).length === 0} onClick={() => save(changes)}>
        {busy ? t('settings.saving') : t('settings.save')}
      </Button>
    </SectionCard>
  )
}

function DurationField({
  id,
  label,
  value,
  options,
  source,
  disabled,
  onChange,
}: {
  id: string
  label: string
  value: string
  options: string[]
  source: Source
  disabled: boolean
  onChange: (v: string) => void
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>
        {label}
        <SourceBadge source={source} />
      </FieldLabel>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o} value={o}>
              {o}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

function DefaultsCard({ res, onSaved }: { res: GetSettingsResponse; onSaved: (r: GetSettingsResponse) => void }) {
  const start = formOf(res)
  const [form, setForm] = useState<Form>(start)
  const { busy, error, save } = useSave(onSaved)
  const locked = lockedReason(res) !== null
  const changes = changesOf(start, form)

  return (
    <SectionCard title={t('settings.defaults')} description={t('settings.defaultsDesc')}>
      <Locked res={res} />
      <Field>
        <FieldLabel htmlFor="settings-drift-policy">
          {t('settings.driftPolicy')}
          <SourceBadge source={sourceOf(res, 'driftPolicy')} />
        </FieldLabel>
        <Select value={form.driftPolicy} onValueChange={(v) => setForm({ ...form, driftPolicy: v })} disabled={locked || busy}>
          <SelectTrigger id="settings-drift-policy" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {DRIFT_POLICIES.map((p) => (
              <SelectItem key={p} value={p}>
                {t(p === 'warnOnly' ? 'settings.driftWarnOnly' : 'settings.driftAutoCorrect')}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {error && <ErrorAlert {...error} />}
      <Button disabled={locked || busy || Object.keys(changes).length === 0} onClick={() => save(changes)}>
        {busy ? t('settings.saving') : t('settings.save')}
      </Button>
    </SectionCard>
  )
}

// UpdatesCard is the switch for the check of newer Ichor releases and what the last
// check reported. A failed check is said here and nowhere on the dashboard.
function UpdatesCard({ res, onSaved }: { res: GetSettingsResponse; onSaved: (r: GetSettingsResponse) => void }) {
  const info = useLoad(() => operationClient.getIchorInstallation({}), []).data
  const effective = res.effective?.updateCheck
  const start = effective?.value === 'false' ? 'off' : 'on'
  const [choice, setChoice] = useState<string>(start)
  const { busy, error, save } = useSave(onSaved)
  const locked = lockedReason(res) !== null
  const source: Source = effective?.source === 'spec' ? 'spec' : 'deployment'
  const checkedAt = info?.releaseCheckedAt

  return (
    <SectionCard title={t('settings.updates')} description={t('settings.updatesDesc')}>
      <Locked res={res} />
      <Field>
        <FieldLabel htmlFor="settings-update-check">
          {t('settings.updateCheck')}
          <SourceBadge source={source} />
        </FieldLabel>
        <Select value={choice} onValueChange={setChoice} disabled={locked || busy}>
          <SelectTrigger id="settings-update-check" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="on">{t('settings.updateCheckOn')}</SelectItem>
            <SelectItem value="off">{t('settings.updateCheckOff')}</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {info?.releaseCheckError && <p className="text-sm text-muted-foreground">{t('settings.updateCheckFailed', { error: info.releaseCheckError })}</p>}
      {checkedAt && <p className="text-sm text-muted-foreground">{t('settings.updateCheckedAt', { time: new Date(Number(checkedAt.seconds) * 1000).toLocaleString() })}</p>}
      {error && <ErrorAlert {...error} />}
      <Button disabled={locked || busy || choice === start} onClick={() => save({ updateCheckEnabled: choice === 'on' })}>
        {busy ? t('settings.saving') : t('settings.save')}
      </Button>
    </SectionCard>
  )
}

// AutoPatchCard is the switch for automatic patch upgrades: off by default, which components, an
// optional daily UTC window, and, while it is halted by a failed or paused automatic operation, the button that
// clears the halt. Clearing is allowed on a read-only deployment too; it is an action, not a setting.
function AutoPatchCard({ res, onSaved }: { res: GetSettingsResponse; onSaved: (r: GetSettingsResponse) => void }) {
  const start = autoPatchFormOf(res)
  const [form, setForm] = useState<AutoPatchForm>(start)
  const { busy, error, save } = useSave(onSaved)
  const locked = lockedReason(res) !== null
  const changes = autoPatchChanges(start, form)
  const problem = autoPatchProblem(form)
  const halted = res.autoPatchHaltedOperation
  const mayClear = res.readOnly || res.canEditSettings
  const componentLabel = (c: string) =>
    t(c === 'talos' ? 'settings.autoPatchComponent.talos' : c === 'cilium' ? 'settings.autoPatchComponent.cilium' : 'settings.autoPatchComponent.kubernetes')

  return (
    <SectionCard title={t('settings.autoPatch')} description={t('settings.autoPatchDesc')}>
      <Locked res={res} />
      {halted && (
        <>
          <Alert variant="destructive">
            <AlertDescription>{t('settings.autoPatchHalted', { operation: halted })}</AlertDescription>
          </Alert>
          <Button variant="outline" disabled={busy || !mayClear} onClick={() => save({ autoPatchAcknowledgedHalt: halted })}>
            {t('settings.autoPatchClear')}
          </Button>
        </>
      )}
      <FieldGroup>
        <Field orientation="horizontal">
          <Switch id="settings-auto-patch" checked={form.enabled} disabled={locked || busy} onCheckedChange={(on: boolean) => setForm({ ...form, enabled: on })} />
          <FieldLabel htmlFor="settings-auto-patch">
            {t('settings.autoPatchEnable')}
            <SourceBadge source={autoPatchSource(res)} />
          </FieldLabel>
        </Field>
        <FieldSet>
          <FieldLegend variant="label">{t('settings.autoPatchComponents')}</FieldLegend>
          <FieldGroup className="gap-2">
            {AUTO_PATCH_COMPONENTS.map((c) => (
              <Field key={c} orientation="horizontal">
                <Checkbox
                  id={`settings-auto-patch-${c}`}
                  checked={form.components.includes(c)}
                  disabled={locked || busy}
                  onCheckedChange={(on: boolean | 'indeterminate') => setForm({ ...form, components: toggleComponent(form.components, c, on === true) })}
                />
                <FieldLabel htmlFor={`settings-auto-patch-${c}`}>{componentLabel(c)}</FieldLabel>
              </Field>
            ))}
          </FieldGroup>
        </FieldSet>
        <Field>
          <FieldLabel htmlFor="settings-auto-patch-window">{t('settings.autoPatchWindow')}</FieldLabel>
          <Input
            id="settings-auto-patch-window"
            value={form.window}
            placeholder="02:00-05:00"
            disabled={locked || busy}
            onInput={(e) => setForm({ ...form, window: (e.currentTarget as HTMLInputElement).value.trim() })}
          />
          <FieldDescription>{t('settings.autoPatchWindowHelp')}</FieldDescription>
        </Field>
      </FieldGroup>
      {problem && <p className="text-sm text-destructive">{t(problem)}</p>}
      {error && <ErrorAlert {...error} />}
      <Button disabled={locked || busy || problem !== null || Object.keys(changes).length === 0} onClick={() => save(changes)}>
        {busy ? t('settings.saving') : t('settings.save')}
      </Button>
    </SectionCard>
  )
}
