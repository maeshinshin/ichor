import { useEffect, useRef, useState } from 'preact/hooks'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { readerLifetimeSeconds, readerLifetimes } from '@/lib/credentials'
import { download } from '@/lib/download'
import { describeError } from '@/lib/errors'
import { notifyDone, notifyFailed } from '@/lib/notify'
import { t } from '@/lib/i18n'

// The os:reader talosconfig a person takes for talosctl (docs/adr/0124). Opened from the cluster's
// "More actions" menu, which offers it only to callers who hold the "download" verb on
// talosclusters/talosconfig-reader; the server and the admission webhook ask again.
export function ReaderTalosconfigDialog({
  cluster,
  open,
  onOpenChange,
}: {
  cluster: { namespace: string; name: string }
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [lifetime, setLifetime] = useState<string>(readerLifetimes[0].key)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => () => abort.current?.abort(), [])

  async function run() {
    onOpenChange(false)
    const controller = new AbortController()
    abort.current = controller
    try {
      const file = await download({ kind: 'reader-talosconfig', cluster, lifetimeSeconds: readerLifetimeSeconds(lifetime) }, controller.signal)
      notifyDone(t('readerTalosconfig.downloaded', { file }))
    } catch (err) {
      if (controller.signal.aborted) return
      const e = describeError(err)
      notifyFailed(t('readerTalosconfig.failed'), e.detail || e.title)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('readerTalosconfig.title')}
      description={t('readerTalosconfig.description')}
      confirm={{ label: t('readerTalosconfig.confirm'), onClick: run, destructive: false }}
    >
      <Field>
        <FieldLabel htmlFor="reader-talosconfig-lifetime">{t('readerTalosconfig.lifetime')}</FieldLabel>
        <Select value={lifetime} onValueChange={setLifetime}>
          <SelectTrigger id="reader-talosconfig-lifetime" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {readerLifetimes.map((l) => (
              <SelectItem key={l.key} value={l.key}>
                {t(`readerTalosconfig.lifetime.${l.key}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
    </ConfirmDialog>
  )
}
