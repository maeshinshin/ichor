import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { InstallDiskPicker } from '@/components/InstallDiskPicker'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import type { Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import type { Disk } from '@/gen/ichor/v1alpha1/node_pb'
import { useAction } from '@/hooks/use-action'
import { machineClient } from '@/lib/connect'
import { notifyWrite } from '@/lib/notify'
import { diskSelector, installChoice } from '@/lib/installDisk'
import { t } from '@/lib/i18n'

// Sets spec.install of an approved machine that is waiting for an install disk (Ready reason
// InstallDiskUnknown, ADR 0076). The disks come from the machine page's own GetNode read.
export function SetInstallDialog({
  machine,
  disks,
  onDone,
}: {
  machine: Machine
  disks: Disk[]
  onDone: () => void
}) {
  const [open, setOpen] = useState(false)
  const [key, setKey] = useState('')
  const [typed, setTyped] = useState('')
  const [image, setImage] = useState('')
  const { busy: submitting, error: submitError, run } = useAction()

  const selectable = disks.filter((d) => diskSelector(d))
  const { install, ok } = installChoice({ disks: selectable, key, typed, image })


  function submit() {
    const ref = machine.ref
    if (!ref || !install) return
    return run(async () => {
      await machineClient.setMachineInstall({ machine: ref, install })
      notifyWrite('toast.installSet')
      setOpen(false)
      onDone()
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">{t('installDisk.button')}</Button>
      </DialogTrigger>
      <DialogContent className="grid-cols-1 sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('installDisk.title', { name: machine.ref?.name ?? '' })}</DialogTitle>
          <DialogDescription>{t('installDisk.description')}</DialogDescription>
        </DialogHeader>
        <div class="space-y-3 text-sm">
          <InstallDiskPicker
            idPrefix="set"
            disks={selectable}
            value={key}
            typed={typed}
            image={image}
            typedOk={ok}
            showImage={!!install}
            onValue={setKey}
            onTyped={setTyped}
            onImage={setImage}
          />
          {submitError && <ErrorAlert {...submitError} />}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button disabled={!install || !ok || submitting} onClick={submit}>
            {submitting ? t('installDisk.submitting') : t('installDisk.button')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
