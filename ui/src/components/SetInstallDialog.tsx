import { useState } from 'preact/hooks'
import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
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
import { offersBreakGlass } from '@/lib/breakGlass'
import { machineClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { diskSelector, installChoice } from '@/lib/installDisk'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }

// Sets spec.install of an approved machine that is waiting for an install disk (Ready reason
// InstallDiskUnknown, ADR 0076). The disks come from the machine page's own GetNode read.
export function SetInstallDialog({
  machine,
  disks,
  onDone,
  onPullRequest,
}: {
  machine: Machine
  disks: Disk[]
  onDone: () => void
  onPullRequest: (url: string, run: BreakGlassRun) => void
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [key, setKey] = useState('')
  const [typed, setTyped] = useState('')
  const [image, setImage] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)
  const [refused, setRefused] = useState(false)

  const selectable = disks.filter((d) => diskSelector(d))
  const { install, ok } = installChoice({ disks: selectable, key, typed, image })

  // The same write again with break_glass set (docs/adr/0065).
  const breakGlassSet: BreakGlassRun = async (bg) => {
    const res = await machineClient.setMachineInstall({ machine: machine.ref, install, breakGlass: bg })
    return { pullRequestUrl: res.pullRequestUrl, notice: res.notice }
  }

  async function submit() {
    if (!machine.ref || !install) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      const res = await machineClient.setMachineInstall({ machine: machine.ref, install })
      if (res.pullRequestUrl) onPullRequest(res.pullRequestUrl, breakGlassSet)
      setOpen(false)
      onDone()
    } catch (err) {
      setSubmitError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">{t('installDisk.button')}</Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
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
          {refused && <BreakGlassDialog run={breakGlassSet} onDone={() => setOpen(false)} />}
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
