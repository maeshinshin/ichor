import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { loginBody, loginErrorKey, loginReady, redirectTarget, ticketFromSearch } from '@/lib/login'
import { useT } from '@/lib/useT'

// The page ichor-ui's authorize endpoint sends the browser to: /login?ticket=...
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
            <form class="space-y-4" onSubmit={submit}>
              <div class="space-y-1">
                <Label htmlFor="login-user">{t('login.username')}</Label>
                <Input id="login-user" autoComplete="username" autoCapitalize="none" spellcheck={false} autoFocus value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
              </div>
              <div class="space-y-1">
                <Label htmlFor="login-password">{t('login.password')}</Label>
                <Input id="login-password" type="password" autoComplete="current-password" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
              </div>
              {error && <ErrorAlert title={t('app.signInFailed')} detail={error} />}
              <Button type="submit" class="w-full" disabled={!loginReady({ username, password }) || busy}>
                {busy ? t('login.submitting') : t('login.submit')}
              </Button>
            </form>
          ) : (
            <Button class="w-full" variant="outline" onClick={() => window.location.assign('/')}>
              {t('login.backHome')}
            </Button>
          )}
        </CardContent>
      </Card>
    </main>
  )
}
