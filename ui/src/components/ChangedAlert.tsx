import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { t } from '@/lib/i18n'

// Shown when a save was refused because the object changed since the page loaded it.
export function ChangedAlert({ detail, onReload }: { detail: string; onReload: () => void }) {
  return (
    <Alert variant="destructive" className="mt-6">
      <AlertTitle>{t('edit.changedTitle')}</AlertTitle>
      <AlertDescription>{detail}</AlertDescription>
      <Button variant="outline" size="sm" className="mt-2" onClick={onReload}>
        {t('edit.reload')}
      </Button>
    </Alert>
  )
}
