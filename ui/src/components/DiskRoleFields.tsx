import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { formatBytes } from '@/lib/format'
import type { Key } from '@/lib/i18n'
import { diskKey, diskSelector, type DiskOption } from '@/lib/installDisk'
import type { DiskRolesChoice, DiskRolesForm } from '@/lib/machineSettings'
import { useT } from '@/lib/useT'

const NONE = 'none'

// Disk roles other than the system disk: EPHEMERAL on its own disk and any number of user volumes,
// picked from the node's own disks. The form state lives in the approval form (see diskRolesChoice
// in lib/machineSettings).
export function DiskRoleFields({
  idPrefix,
  disks,
  form,
  errors,
  onChange,
}: {
  idPrefix: string
  // disks already filtered to those that can be selected (selectableDisks).
  disks: DiskOption[]
  form: DiskRolesForm
  errors: DiskRolesChoice['errors']
  onChange: (f: DiskRolesForm) => void
}) {
  const t = useT()
  const err = (k: Key | undefined) => k && <p class="text-destructive">{t(k)}</p>
  const option = (d: DiskOption) => (
    <SelectItem key={diskKey(d)} value={diskKey(d)}>
      {t('disks.option', {
        id: Object.values(diskSelector(d) ?? {})[0] ?? '',
        size: formatBytes(d.sizeBytes),
        dev: d.devPath || '-',
      })}
    </SelectItem>
  )
  const setVolume = (i: number, patch: Partial<DiskRolesForm['volumes'][number]>) =>
    onChange({ ...form, volumes: form.volumes.map((v, j) => (j === i ? { ...v, ...patch } : v)) })

  if (disks.length === 0) {
    return (
      <section class="space-y-1">
        <h4 class="font-medium">{t('disks.title')}</h4>
        <p class="text-muted-foreground">{t('disks.needList')}</p>
      </section>
    )
  }

  return (
    <section class="space-y-2">
      <h4 class="font-medium">{t('disks.title')}</h4>
      <p class="text-muted-foreground">{t('disks.help')}</p>

      <div class="space-y-1">
        <Label htmlFor={`${idPrefix}-ephemeral`}>{t('disks.ephemeral')}</Label>
        <Select value={form.ephemeral || NONE} onValueChange={(v) => onChange({ ...form, ephemeral: v === NONE ? '' : v })}>
          <SelectTrigger id={`${idPrefix}-ephemeral`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NONE}>{t('disks.none')}</SelectItem>
            {disks.map(option)}
          </SelectContent>
        </Select>
        {err(errors.ephemeral)}
      </div>

      {form.volumes.length > 0 && <h5 class="font-medium">{t('disks.volumes')}</h5>}
      {form.volumes.map((v, i) => (
        <div key={i} class="space-y-1 rounded-md border p-2">
          <Label htmlFor={`${idPrefix}-vol-${i}-name`}>{t('disks.volumeName')}</Label>
          <Input
            id={`${idPrefix}-vol-${i}-name`}
            value={v.name}
            onInput={(e) => setVolume(i, { name: (e.target as HTMLInputElement).value })}
          />
          <Label htmlFor={`${idPrefix}-vol-${i}-disk`}>{t('disks.volumeDisk')}</Label>
          <Select value={v.key || NONE} onValueChange={(k) => setVolume(i, { key: k === NONE ? '' : k })}>
            <SelectTrigger id={`${idPrefix}-vol-${i}-disk`} className="w-full">
              <SelectValue placeholder={t('disks.selectDisk')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>{t('disks.selectDisk')}</SelectItem>
              {disks.map(option)}
            </SelectContent>
          </Select>
          <Label htmlFor={`${idPrefix}-vol-${i}-size`}>{t('disks.volumeSize')}</Label>
          <Input
            id={`${idPrefix}-vol-${i}-size`}
            value={v.size}
            placeholder="200GiB"
            onInput={(e) => setVolume(i, { size: (e.target as HTMLInputElement).value })}
          />
          <p class="text-muted-foreground">{t('disks.volumeSizeHelp')}</p>
          {err(errors.volumes[i])}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onChange({ ...form, volumes: form.volumes.filter((_, j) => j !== i) })}
          >
            {t('disks.removeVolume')}
          </Button>
        </div>
      ))}
      <Button variant="outline" size="sm" onClick={() => onChange({ ...form, volumes: [...form.volumes, { key: '', name: '', size: '' }] })}>
        {t('disks.addVolume')}
      </Button>
    </section>
  )
}
