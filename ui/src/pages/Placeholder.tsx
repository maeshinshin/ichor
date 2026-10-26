import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useT } from '@/lib/useT'

// Every phase-5 screen is a stub: this is the only page component until
// later phases fill in cluster/node/config/log/GitOps content.
export function Placeholder({ title }: { title: string }) {
  const t = useT()
  return (
    <Card className="mx-auto mt-10 max-w-xl">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <p class="text-sm text-muted-foreground">{t('app.comingSoon')}</p>
      </CardContent>
    </Card>
  )
}
