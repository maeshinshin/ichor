import { useState } from 'preact/hooks'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import type { StoragePreset } from '@/gen/ichor/v1alpha1/storage_pb'
import { useAction } from '@/hooks/use-action'
import { storageClient } from '@/lib/connect'
import { t } from '@/lib/i18n'
import { notifyDone } from '@/lib/notify'

// CreateStorageDialog writes one TalosStorage as the signed-in user (StorageService.CreateStorage). Only a
// preset the server marks creatable can be chosen; the others stay listed, with the reason they are not made here.
export function CreateStorageDialog({ cluster, presets, onDone }: { cluster?: ObjectRef; presets: StoragePreset[]; onDone: () => void }) {
  const first = presets.find((p) => p.creatable)?.name ?? ''
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [nodeSelector, setNodeSelector] = useState('')
  const [preset, setPreset] = useState(first)
  const { busy, error, setError, run } = useAction()

  const chosen = presets.find((p) => p.name === preset)
  const ready = name !== '' && nodeSelector.trim() !== '' && chosen?.creatable === true

  function reset() {
    setName('')
    setNodeSelector('')
    setPreset(first)
    setError(null)
  }

  function change(next: boolean) {
    setOpen(next)
    if (!next) reset()
  }

  function submit() {
    return run(async () => {
      await storageClient.createStorage({ cluster, name, diskPreset: preset, nodeSelector: nodeSelector.trim() })
      onDone()
      notifyDone(t('storage.created', { name }))
      change(false)
    })
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        {t('storage.create')}
      </Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('storage.createTitle')}</DialogTitle>
            <DialogDescription>{t('storage.createIntro')}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="storage-name">{t('storage.name')}</FieldLabel>
              <Input id="storage-name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="storage-nodes">{t('storage.nodes')}</FieldLabel>
              <Input
                id="storage-nodes"
                placeholder="storage=rook"
                value={nodeSelector}
                onInput={(e) => setNodeSelector((e.target as HTMLInputElement).value)}
              />
              <FieldDescription>{t('storage.nodesHint')}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="storage-preset">{t('storage.preset')}</FieldLabel>
              <NativeSelect id="storage-preset" value={preset} onChange={(e) => setPreset((e.target as HTMLSelectElement).value)}>
                {presets.map((p) => (
                  <NativeSelectOption key={p.name} value={p.name} disabled={!p.creatable}>
                    {p.creatable ? p.name : `${p.name} (${t('storage.notHere')})`}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            {chosen && !chosen.creatable && (
              <Alert>
                <AlertDescription>{chosen.unavailableReason}</AlertDescription>
              </Alert>
            )}
          </FieldGroup>
          {error && <ErrorAlert {...error} />}
          <DialogFooter>
            <Button variant="outline" onClick={() => change(false)}>
              {t('common.cancel')}
            </Button>
            <Button disabled={!ready || busy} onClick={submit}>
              {t('storage.submit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
