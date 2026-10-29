import { useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { BoxesIcon, KeyRoundIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { startLogin, type AuthProvider } from '@/lib/oidc'
import { useT } from '@/lib/useT'

// The sign-in screen: one button per provider /auth/config lists. "Sign in with
// Ichor" goes to ichor-ui's own issuer (whose login form is the /login page), the other one to the
// external identity provider.
export function SignIn({ providers }: { providers: AuthProvider[] }) {
  const t = useT()
  const [error, setError] = useState('')
  const ordered = [...providers].sort((a, b) => Number(b.id === 'ichor') - Number(a.id === 'ichor'))

  return (
    <main class="mx-auto flex min-h-dvh max-w-sm flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle>{t('signin.title')}</CardTitle>
          <CardDescription>{t('signin.description')}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {ordered.map((p, i) => (
            <Button
              key={p.id}
              className="w-full"
              variant={i === 0 ? 'default' : 'outline'}
              onClick={() => startLogin(p).catch((e) => setError(e instanceof Error ? e.message : t('app.loginFailed')))}
            >
              {p.id === 'ichor' ? <BoxesIcon data-icon="inline-start" aria-hidden="true" /> : <KeyRoundIcon data-icon="inline-start" aria-hidden="true" />}
              {p.id === 'ichor' ? t('signin.ichor') : t('signin.oidc')}
            </Button>
          ))}
          {error && <ErrorAlert title={t('app.signInFailed')} detail={error} />}
        </CardContent>
      </Card>
    </main>
  )
}
