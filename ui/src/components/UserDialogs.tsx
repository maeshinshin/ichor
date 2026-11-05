import { useState } from 'preact/hooks'
import { CheckIcon, CopyIcon } from 'lucide-preact'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '@/components/ui/input-group'
import type { User } from '@/gen/ichor/v1alpha1/user_pb'
import { userClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import { minPasswordLength, passwordOk, roleNames, toggleRole, userError, userNameOk } from '@/lib/users'
import { useAction } from '@/hooks/use-action'
import { t } from '@/lib/i18n'

function RolePicker({ id, roles, onChange }: { id: string; roles: string[]; onChange: (roles: string[]) => void }) {
  return (
    <FieldSet>
      <FieldLegend variant="label">{t('users.roles')}</FieldLegend>
      <FieldGroup className="gap-2">
        {roleNames.map((r) => (
          <Field key={r} orientation="horizontal">
            <Checkbox id={`${id}-${r}`} checked={roles.includes(r)} onCheckedChange={(on: boolean | 'indeterminate') => onChange(toggleRole(roles, r, on === true))} />
            <FieldLabel htmlFor={`${id}-${r}`}>{t(`users.role.${r}`)}</FieldLabel>
          </Field>
        ))}
      </FieldGroup>
      {roles.length === 0 && <FieldDescription>{t('users.rolesNone')}</FieldDescription>}
    </FieldSet>
  )
}

type ProfileProps = { id: string; displayName: string; email: string; setDisplayName: (v: string) => void; setEmail: (v: string) => void }

function ProfileFields({ id, displayName, email, setDisplayName, setEmail }: ProfileProps) {
  return (
    <>
      <Field>
        <FieldLabel htmlFor={`${id}-display`}>{t('users.displayName')}</FieldLabel>
        <Input id={`${id}-display`} value={displayName} onInput={(e) => setDisplayName((e.target as HTMLInputElement).value)} />
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-email`}>{t('users.email')}</FieldLabel>
        <Input id={`${id}-email`} type="email" value={email} onInput={(e) => setEmail((e.target as HTMLInputElement).value)} />
      </Field>
    </>
  )
}

// GeneratedPassword shows a password the server made, once, with a copy button.
function GeneratedPassword({ password }: { password: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Field>
      <FieldDescription>{t('users.generatedOnce')}</FieldDescription>
      <InputGroup>
        <InputGroupInput readOnly value={password} aria-label={t('users.passwordGenerated')} onFocus={(e) => (e.target as HTMLInputElement).select()} />
        <InputGroupAddon align="inline-end">
          <InputGroupButton
            type="button"
            onClick={() => {
              navigator.clipboard?.writeText(password).then(() => setCopied(true), () => {})
            }}
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? t('users.copied') : t('users.copy')}
          </InputGroupButton>
        </InputGroupAddon>
      </InputGroup>
    </Field>
  )
}

// Create user: CreateUser, then SetPassword (typed, or empty so the server generates one and
// returns it once). SetPassword always marks the password must-change, so the dialog says so
// instead of offering a switch that would do nothing.
export function CreateUserDialog({ onDone }: { onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [roles, setRoles] = useState<string[]>(['viewer'])
  const [typePassword, setTypePassword] = useState(false)
  const [password, setPassword] = useState('')
  const { busy, error, setError, run } = useAction(userError)
  const [generated, setGenerated] = useState<{ name: string; password: string } | null>(null)

  const nameBad = name !== '' && !userNameOk(name)
  const ready = userNameOk(name) && (!typePassword || passwordOk(password))

  function reset() {
    setName('')
    setDisplayName('')
    setEmail('')
    setRoles(['viewer'])
    setTypePassword(false)
    setPassword('')
    setError(null)
    setGenerated(null)
  }

  function change(next: boolean) {
    setOpen(next)
    if (!next) reset()
  }

  function submit() {
    let created = false
    return run(
      async () => {
        await userClient.createUser({ name, displayName, email, roles })
        created = true
        const res = await userClient.setPassword({ name, password: typePassword ? password : '' })
        onDone()
        if (res.generatedPassword) return setGenerated({ name, password: res.generatedPassword })
        notifyDone(t('users.created', { name }))
        change(false)
      },
      (err) => {
        if (!created) return
        const e = userError(err)
        onDone()
        setError({ title: e.title, detail: `${t('users.createdNoPassword', { name })} ${e.detail}` })
      },
    )
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        {t('users.create')}
      </Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
          {generated ? (
            <>
              <DialogHeader>
                <DialogTitle>{t('users.created', { name: generated.name })}</DialogTitle>
                <DialogDescription>{t('users.mustChange')}</DialogDescription>
              </DialogHeader>
              <GeneratedPassword password={generated.password} />
              <DialogFooter>
                <Button onClick={() => change(false)}>{t('common.close')}</Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <DialogHeader>
                <DialogTitle>{t('users.createTitle')}</DialogTitle>
                <DialogDescription>{t('users.createDescription')}</DialogDescription>
              </DialogHeader>
              <FieldGroup>
                <Field data-invalid={nameBad}>
                  <FieldLabel htmlFor="user-name">{t('users.name')}</FieldLabel>
                  <Input id="user-name" autoCapitalize="none" spellcheck={false} aria-invalid={nameBad} value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
                  {nameBad ? <FieldError>{t('users.nameInvalid')}</FieldError> : <FieldDescription>{t('users.nameHint')}</FieldDescription>}
                </Field>
                <ProfileFields id="user" displayName={displayName} email={email} setDisplayName={setDisplayName} setEmail={setEmail} />
                <RolePicker id="create-role" roles={roles} onChange={setRoles} />
                <FieldSet>
                  <FieldLegend variant="label">{t('users.passwordMode')}</FieldLegend>
                  <Field orientation="horizontal">
                    <Checkbox id="user-typed" checked={typePassword} onCheckedChange={(on: boolean | 'indeterminate') => setTypePassword(on === true)} />
                    <FieldLabel htmlFor="user-typed">{t('users.passwordType')}</FieldLabel>
                  </Field>
                  {typePassword ? (
                    <Field>
                      <Input type="password" autoComplete="new-password" aria-label={t('users.passwordType')} value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
                      <FieldDescription>{t('account.passwordHint', { n: minPasswordLength })}</FieldDescription>
                    </Field>
                  ) : (
                    <FieldDescription>{t('users.passwordGenerate')}</FieldDescription>
                  )}
                  <FieldDescription>{t('users.mustChange')}</FieldDescription>
                </FieldSet>
                {error && <ErrorAlert {...error} />}
              </FieldGroup>
              <DialogFooter>
                <Button variant="ghost" onClick={() => change(false)}>
                  {t('common.cancel')}
                </Button>
                <Button disabled={!ready || busy} onClick={submit}>
                  {busy ? t('users.creating') : t('users.create')}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

export function EditUserDialog({ user, open, onOpenChange, onDone }: { user: User; open: boolean; onOpenChange: (o: boolean) => void; onDone: () => void }) {
  const [displayName, setDisplayName] = useState(user.displayName)
  const [email, setEmail] = useState(user.email)
  const [roles, setRoles] = useState<string[]>(user.roles)
  const [disabled, setDisabled] = useState(user.disabled)
  const { busy, error, run } = useAction(userError)

  const submit = () =>
    run(async () => {
      await userClient.updateUser({ name: user.name, displayName, email, roles, disabled })
      notifyDone(t('users.updated', { name: user.name }))
      onOpenChange(false)
      onDone()
    })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('users.editTitle', { name: user.name })}</DialogTitle>
        </DialogHeader>
        <FieldGroup>
          <ProfileFields id="edit" displayName={displayName} email={email} setDisplayName={setDisplayName} setEmail={setEmail} />
          <RolePicker id="edit-role" roles={roles} onChange={setRoles} />
          <Field orientation="horizontal">
            <Checkbox id="edit-disabled" checked={disabled} onCheckedChange={(on: boolean | 'indeterminate') => setDisabled(on === true)} />
            <FieldLabel htmlFor="edit-disabled">{t('users.disabled')}</FieldLabel>
            <FieldDescription>{t('users.disabledHint')}</FieldDescription>
          </Field>
          {error && <ErrorAlert {...error} />}
        </FieldGroup>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button disabled={busy} onClick={submit}>
            {busy ? t('users.saving') : t('users.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function SetPasswordDialog({ user, open, onOpenChange }: { user: User; open: boolean; onOpenChange: (o: boolean) => void }) {
  const [password, setPassword] = useState('')
  const { busy, error, setError, run } = useAction(userError)
  const [generated, setGenerated] = useState('')

  function change(next: boolean) {
    onOpenChange(next)
    if (!next) {
      setPassword('')
      setGenerated('')
      setError(null)
    }
  }

  const submit = () =>
    run(async () => {
      const res = await userClient.setPassword({ name: user.name, password })
      if (res.generatedPassword) return setGenerated(res.generatedPassword)
      notifyDone(t('users.passwordSet', { name: user.name }))
      change(false)
    })

  return (
    <Dialog open={open} onOpenChange={change}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('users.setPasswordTitle', { name: user.name })}</DialogTitle>
          <DialogDescription>{t('users.setPasswordDescription')}</DialogDescription>
        </DialogHeader>
        {generated ? (
          <GeneratedPassword password={generated} />
        ) : (
          <Field>
            <FieldLabel htmlFor="set-password">{t('users.newPasswordOptional')}</FieldLabel>
            <Input id="set-password" type="password" autoComplete="new-password" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
            <FieldDescription>{t('account.passwordHint', { n: minPasswordLength })}</FieldDescription>
          </Field>
        )}
        {error && <ErrorAlert {...error} />}
        <DialogFooter>
          {generated ? (
            <Button onClick={() => change(false)}>{t('common.close')}</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => change(false)}>
                {t('common.cancel')}
              </Button>
              <Button disabled={busy || (password !== '' && !passwordOk(password))} onClick={submit}>
                {t('users.setPassword')}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// Revoke sessions and delete go through the alert dialog; the server's refusal (the last enabled
// admin, a missing permission) is shown inside it.
export function RevokeUserDialog({ user, open, onOpenChange }: { user: User; open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <UserConfirm
      open={open}
      onOpenChange={onOpenChange}
      title={t('users.revokeTitle', { name: user.name })}
      description={t('users.revokeDescription')}
      label={t('users.revoke')}
      run={async () => {
        await userClient.revokeSessions({ name: user.name })
        notifyDone(t('users.revoked', { name: user.name }))
      }}
    />
  )
}

export function DeleteUserDialog({ user, open, onOpenChange, onDone }: { user: User; open: boolean; onOpenChange: (o: boolean) => void; onDone: () => void }) {
  return (
    <UserConfirm
      open={open}
      onOpenChange={onOpenChange}
      title={t('users.deleteTitle', { name: user.name })}
      description={t('users.deleteDescription')}
      label={t('users.delete')}
      run={async () => {
        await userClient.deleteUser({ name: user.name })
        notifyDone(t('users.deleted', { name: user.name }))
        onDone()
      }}
    />
  )
}

function UserConfirm({
  open,
  onOpenChange,
  title,
  description,
  label,
  run,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  title: string
  description: string
  label: string
  run: () => Promise<void>
}) {
  const { busy, error, setError, run: act } = useAction(userError)

  const go = () =>
    act(async () => {
      await run()
      onOpenChange(false)
    })

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o)
        if (!o) setError(null)
      }}
      title={title}
      description={description}
      confirm={{ label, disabled: busy, onClick: go }}
    >
      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
