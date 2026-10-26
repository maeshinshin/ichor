// Pure helpers for the break-glass dialog (docs/adr/0065). No import from
// "@/gen": these run directly under `node --test` (docs/adr/0045).
import { Code, ConnectError } from '@connectrpc/connect'

// A reason is required: it is recorded in the audit record, the Event and the
// pull request. The server refuses a blank one too; this stops the click.
export function reasonIsValid(reason: string): boolean {
  return reason.trim() !== ''
}

export function breakGlassRequest(reason: string): { reason: string } {
  return { reason: reason.trim() }
}

// offersBreakGlass is true where the answer was a pull request or a refusal.
// The UI does not ask beforehand whether the user may (no extra RPC): it
// offers the path and shows the server's reason if the user is turned down.
export function offersBreakGlass(outcome: { pullRequestUrl?: string; error?: unknown }): boolean {
  if (outcome.pullRequestUrl) return true
  const err = outcome.error
  return err instanceof ConnectError && (err.code === Code.FailedPrecondition || err.code === Code.PermissionDenied)
}
