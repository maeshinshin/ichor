import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'

// Shared rendering for describeError()'s {title, detail} shape - every
// screen that calls the backend needs this, not just the approval flow.
export function ErrorAlert({ title, detail }: { title: string; detail: string }) {
  return (
    <Alert variant="destructive" className="mt-6">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{detail}</AlertDescription>
    </Alert>
  )
}
