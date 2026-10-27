import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { userClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import { minPasswordLength, passwordError, passwordOk } from '@/lib/users'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }

// The change-own-password form (docs/adr/0094), used in the account menu's dialog and, with nothing
// else on the screen, when the account must change its password. Neither password is kept anywhere
// but in these fields, and they are cleared once the call succeeds.
export function ChangePasswordForm({ onDone, onCancel }: { onDone: () => void; onCancel?: () => void }) {
  const t = useT()
  const [oldPassword, setOld] = useState('')
  const [next, setNext] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  const mismatch = again !== '' && again !== next
  const ready = oldPassword !== '' && passwordOk(next) && next === again

  async function submit(e: Event) {
    e.preventDefault()
    if (!ready || busy) return
    setBusy(true)
    setError(null)
    try {
      await userClient.changeOwnPassword({ oldPassword, newPassword: next })
      setOld('')
      setNext('')
      setAgain('')
      notifyDone(t('account.passwordChanged'))
      onDone()
    } catch (err) {
      setError(passwordError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form class="space-y-4" onSubmit={submit}>
      <div class="space-y-1">
        <Label htmlFor="pw-old">{t('account.oldPassword')}</Label>
        <Input id="pw-old" type="password" autoComplete="current-password" value={oldPassword} onInput={(e) => setOld((e.target as HTMLInputElement).value)} />
      </div>
      <div class="space-y-1">
        <Label htmlFor="pw-new">{t('account.newPassword')}</Label>
        <Input id="pw-new" type="password" autoComplete="new-password" value={next} onInput={(e) => setNext((e.target as HTMLInputElement).value)} />
        <p class="text-xs text-muted-foreground">{t('account.passwordHint', { n: minPasswordLength })}</p>
      </div>
      <div class="space-y-1">
        <Label htmlFor="pw-again">{t('account.confirmPassword')}</Label>
        <Input id="pw-again" type="password" autoComplete="new-password" aria-invalid={mismatch} value={again} onInput={(e) => setAgain((e.target as HTMLInputElement).value)} />
        {mismatch && <p class="text-xs text-destructive">{t('account.mismatch')}</p>}
      </div>
      {error && <ErrorAlert {...error} />}
      <div class="flex justify-end gap-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t('common.cancel')}
          </Button>
        )}
        <Button type="submit" disabled={!ready || busy}>
          {busy ? t('account.saving') : t('account.save')}
        </Button>
      </div>
    </form>
  )
}

export function ChangePasswordDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t('account.changePassword')}</DialogTitle>
          <DialogDescription>{t('account.passwordHint', { n: minPasswordLength })}</DialogDescription>
        </DialogHeader>
        <ChangePasswordForm onDone={() => onOpenChange(false)} onCancel={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

// MustChangePassword blocks the whole app until the password is changed.
export function MustChangePassword({ onDone }: { onDone: () => void }) {
  const t = useT()
  return (
    <main class="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 p-6">
      <div class="space-y-1">
        <h1 class="text-xl font-semibold tracking-tight">{t('account.mustChangeTitle')}</h1>
        <p class="text-sm text-muted-foreground">{t('account.mustChangeBody')}</p>
      </div>
      <ChangePasswordForm onDone={onDone} />
    </main>
  )
}
