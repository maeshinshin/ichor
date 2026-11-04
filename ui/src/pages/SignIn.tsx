import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { KeyRoundIcon, LogInIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { LoginError, loginReady } from '@/lib/login'
import { startLogin, type AuthProvider } from '@/lib/oidc'
import { t } from '@/lib/i18n'

// The one sign-in card (docs/adr/0094): the user name / password form of ichor-ui's own issuer,
// and below it, when configured, the button for the external identity provider of ADR 0058 (the
// only part that leaves the page). The same card is the SPA's sign-in and the /login?ticket= page
// the CLI's browser flow lands on; `signIn` is what differs. The password lives only in this form's
// state and the request body: never in a URL, storage or a log, and a failed attempt clears it.
export function SignIn({ ichor, oidc, signIn }: { ichor: boolean; oidc: AuthProvider | null; signIn: (f: { username: string; password: string }) => Promise<void> }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function submit(e: Event) {
    e.preventDefault()
    if (!loginReady({ username, password }) || busy) return
    setBusy(true)
    setError('')
    try {
      await signIn({ username, password })
    } catch (err) {
      setError(t(err instanceof LoginError ? err.key : 'login.unavailable'))
      setPassword('')
      setBusy(false)
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle>{t('signin.title')}</CardTitle>
          {ichor && <CardDescription>{t('login.description')}</CardDescription>}
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {ichor && (
            <form onSubmit={submit}>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="login-user">{t('login.username')}</FieldLabel>
                  <Input id="login-user" autoComplete="username" autoCapitalize="none" spellcheck={false} autoFocus value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="login-password">{t('login.password')}</FieldLabel>
                  <Input id="login-password" type="password" autoComplete="current-password" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
                </Field>
                <Button type="submit" className="w-full" disabled={!loginReady({ username, password }) || busy}>
                  <LogInIcon data-icon="inline-start" aria-hidden="true" />
                  {busy ? t('login.submitting') : t('login.submit')}
                </Button>
              </FieldGroup>
            </form>
          )}
          {ichor && oidc && <Separator />}
          {oidc && (
            <Button
              className="w-full"
              variant="outline"
              onClick={() => startLogin(oidc).catch((e) => setError(e instanceof Error ? e.message : t('app.loginFailed')))}
            >
              <KeyRoundIcon data-icon="inline-start" aria-hidden="true" />
              {t('signin.oidc')}
            </Button>
          )}
          {error && <ErrorAlert title={t('app.signInFailed')} detail={error} />}
        </CardContent>
      </Card>
    </main>
  )
}
