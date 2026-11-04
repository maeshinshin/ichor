import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ArrowLeftIcon } from 'lucide-preact'
import { postLogin, ticketFromSearch } from '@/lib/login'
import { SignIn } from '@/pages/SignIn'
import { t } from '@/lib/i18n'

// The page ichor-ui's authorize endpoint sends a browser to (docs/adr/0094): /login?ticket=... -
// what `ichor auth login` opens. It shows the same card as the SPA's own sign-in, posts the form to
// /oidc/login and follows the redirect_uri that comes back (the CLI's loopback callback).
export function Login() {
  const ticket = ticketFromSearch(window.location.search)
  if (ticket) return <SignIn ichor oidc={null} signIn={async (f) => window.location.assign(await postLogin(ticket, f))} />
  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle>{t('signin.title')}</CardTitle>
          <CardDescription>{t('login.noTicket')}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button className="w-full" variant="outline" onClick={() => window.location.assign('/')}>
            <ArrowLeftIcon data-icon="inline-start" aria-hidden="true" />
            {t('login.backHome')}
          </Button>
        </CardContent>
      </Card>
    </main>
  )
}
