import { Code, ConnectError } from '@connectrpc/connect'
import { t } from './i18n.ts'

// Turns a Connect error into something a person reads, instead of the raw
// wire error (the task's own instruction for the approve/reject screen,
// but just as true for every other call this UI makes). The server
// (internal/uiapi) already writes its refusal messages in plain English
// (see ApproveMachine/RejectMachine, ADR 0018); this only adds a short,
// code-specific headline above that message so the kind of problem is
// clear at a glance before reading the detail. The headline is in the current
// language (ADR 0064); the server's message is never translated.
export type ErrorShape = { title: string; detail: string }

// isOverridable is true when the server marked a refusal as one the override verb lifts (ADR 0110): the
// Ichor-Overridable response header. The dialog then offers its override option.
export function isOverridable(err: unknown): boolean {
  return err instanceof ConnectError && err.metadata.get('Ichor-Overridable') === 'true'
}

export function describeError(err: unknown): ErrorShape {
  if (err instanceof ConnectError) {
    switch (err.code) {
      case Code.FailedPrecondition:
      case Code.PermissionDenied:
        return { title: t('error.notAllowed'), detail: err.rawMessage }
      case Code.InvalidArgument:
        return { title: t('error.invalidInput'), detail: err.rawMessage }
      case Code.NotFound:
        return { title: t('common.notFound'), detail: err.rawMessage }
      case Code.Unavailable:
        return { title: t('error.unavailable'), detail: t('error.unavailableDetail') }
      default:
        return { title: t('error.requestFailed'), detail: err.rawMessage }
    }
  }
  return { title: t('error.requestFailed'), detail: err instanceof Error ? err.message : String(err) }
}
