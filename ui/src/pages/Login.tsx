import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ArrowLeftIcon, LogInIcon } from 'lucide-preact'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { loginBody, loginErrorKey, loginReady, redirectTarget, ticketFromSearch } from '@/lib/login'
import { useT } from '@/lib/useT'

// The page ichor-ui's authorize endpoint sends the browser to (docs/adr/0094): /login?ticket=...
// It posts the form to /oidc/login and follows the redirect_uri that comes back, which returns to
// /auth/callback with the authorization code. The password is only in this form's state and the
// request body: never in a URL, storage or a log, and a failed attempt clears it.
export function Login() {
  const t = useT()
  const ticket = ticketFromSearch(window.location.search)
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
      const res = await fetch('/oidc/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: loginBody(ticket, { username, password }),
      })
      if (!res.ok) {
        setError(t(loginErrorKey(res.status)))
        setPassword('')
        return
      }
      const target = redirectTarget(await res.json())
      if (!target) {
        setError(t('login.unavailable'))
        return
      }
      window.location.assign(target)
    } catch {
      setError(t('login.unavailable'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <main class="mx-auto flex min-h-dvh max-w-sm flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle>{t('login.title')}</CardTitle>
          <CardDescription>{ticket ? t('login.description') : t('login.noTicket')}</CardDescription>
        </CardHeader>
        <CardContent>
          {ticket ? (
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
              {error && <ErrorAlert title={t('app.signInFailed')} detail={error} />}
              <Button type="submit" className="w-full" disabled={!loginReady({ username, password }) || busy}>
                <LogInIcon data-icon="inline-start" aria-hidden="true" />
                {busy ? t('login.submitting') : t('login.submit')}
              </Button>
              </FieldGroup>
            </form>
          ) : (
            <Button className="w-full" variant="outline" onClick={() => window.location.assign('/')}>
              <ArrowLeftIcon data-icon="inline-start" aria-hidden="true" />
              {t('login.backHome')}
            </Button>
          )}
        </CardContent>
      </Card>
    </main>
  )
}
