import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { formatBytes } from '@/lib/format'
import { diskKey, diskSelector, type DiskOption } from '@/lib/installDisk'
import { t } from '@/lib/i18n'

// The install disk choice shared by the approval form and the machine page's "set install disk":
// a disk from the list the node reported (discovery's, or the node's own), otherwise a typed device
// path. The state lives in the parent (see installChoice in lib/installDisk for turning it into the
// install target).
export function InstallDiskPicker({
  idPrefix,
  disks,
  value,
  defaulted,
  typed,
  image,
  typedOk,
  showImage,
  onValue,
  onTyped,
  onImage,
}: {
  idPrefix: string
  // disks already filtered to those that can be selected (diskSelector set).
  disks: DiskOption[]
  value: string
  // defaulted is true while the form starts on the default disk (defaultInstallDisk), not one the
  // person picked.
  defaulted?: boolean
  typed: string
  image: string
  typedOk: boolean
  showImage: boolean
  onValue: (v: string) => void
  onTyped: (v: string) => void
  onImage: (v: string) => void
}) {
  return (
    <section class="space-y-1">
      <Label htmlFor={`${idPrefix}-install-disk`}>{t('approve.installDisk')}</Label>
      {disks.length > 0 ? (
        <Select value={value || 'none'} onValueChange={(v) => onValue(v === 'none' ? '' : v)}>
          <SelectTrigger id={`${idPrefix}-install-disk`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="none">{t('approve.installDiskNone')}</SelectItem>
            {disks.map((d) => (
              <SelectItem key={diskKey(d)} value={diskKey(d)}>
                {t('approve.installDiskOption', {
                  id: Object.values(diskSelector(d) ?? {})[0] ?? '',
                  size: [formatBytes(d.sizeBytes), d.model].filter(Boolean).join(' '),
                  dev: d.devPath || '-',
                })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={`${idPrefix}-install-disk`}
          value={typed}
          placeholder="/dev/sda"
          onInput={(e) => onTyped((e.target as HTMLInputElement).value)}
          aria-invalid={!typedOk}
        />
      )}
      <p class="text-muted-foreground">
        {disks.length === 0
          ? t('approve.installDiskTyped')
          : defaulted
            ? t('approve.installDiskDefaulted')
            : t('approve.installDiskHelp')}
      </p>
      {showImage && (
        <>
          <Label htmlFor={`${idPrefix}-install-image`}>{t('approve.installImage')}</Label>
          <Input id={`${idPrefix}-install-image`} value={image} onInput={(e) => onImage((e.target as HTMLInputElement).value)} />
        </>
      )}
    </section>
  )
}
