import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { userClient } from '@/lib/connect'
import { signInAgain } from '@/lib/login'
import { clearToken } from '@/lib/oidc'
import { minPasswordLength, passwordError, passwordOk } from '@/lib/users'
import type { ErrorShape } from '@/lib/errors'
import { t } from '@/lib/i18n'

// The change-own-password form (docs/adr/0094), used in the account menu's dialog and, with nothing
// else on the screen, when the account must change its password. Neither password is kept anywhere
// but in these fields, and they are cleared once the call succeeds.
export function ChangePasswordForm({ onCancel }: { onCancel?: () => void }) {
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
      // The change ended every token issued before it, this tab's included (docs/adr/0108), so sign
      // this tab in again with the new password (docs/adr/0094 amendment); other sessions stay
      // ended. If that fails the tab signs out and the person signs in by hand.
      await signInAgain(next).catch(clearToken)
      setOld('')
      setNext('')
      setAgain('')
      window.location.assign('/')
    } catch (err) {
      setError(passwordError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit}>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="pw-old">{t('account.oldPassword')}</FieldLabel>
          <Input id="pw-old" type="password" autoComplete="current-password" value={oldPassword} onInput={(e) => setOld((e.target as HTMLInputElement).value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="pw-new">{t('account.newPassword')}</FieldLabel>
          <Input id="pw-new" type="password" autoComplete="new-password" value={next} onInput={(e) => setNext((e.target as HTMLInputElement).value)} />
          <FieldDescription>{t('account.passwordHint', { n: minPasswordLength })}</FieldDescription>
        </Field>
        <Field data-invalid={mismatch}>
          <FieldLabel htmlFor="pw-again">{t('account.confirmPassword')}</FieldLabel>
          <Input id="pw-again" type="password" autoComplete="new-password" aria-invalid={mismatch} value={again} onInput={(e) => setAgain((e.target as HTMLInputElement).value)} />
          {mismatch && <FieldError>{t('account.mismatch')}</FieldError>}
        </Field>
        {error && <ErrorAlert {...error} />}
        <Field orientation="horizontal" className="justify-end">
          {onCancel && (
            <Button type="button" variant="ghost" onClick={onCancel}>
              {t('common.cancel')}
            </Button>
          )}
          <Button type="submit" disabled={!ready || busy}>
            {busy ? t('account.saving') : t('account.save')}
          </Button>
        </Field>
      </FieldGroup>
    </form>
  )
}

export function ChangePasswordDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('account.changePassword')}</DialogTitle>
          <DialogDescription>{t('account.passwordHint', { n: minPasswordLength })}</DialogDescription>
        </DialogHeader>
        <ChangePasswordForm onCancel={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

// MustChangePassword blocks the whole app until the password is changed.
export function MustChangePassword() {
  return (
    <main class="mx-auto flex min-h-dvh max-w-md flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle>{t('account.mustChangeTitle')}</CardTitle>
          <CardDescription>{t('account.mustChangeBody')}</CardDescription>
        </CardHeader>
        <CardContent>
          <ChangePasswordForm />
        </CardContent>
      </Card>
    </main>
  )
}
