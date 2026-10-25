import { Code, ConnectError } from '@connectrpc/connect'

// Turns a Connect error into something a person reads, instead of the raw
// wire error (the task's own instruction for the approve/reject screen,
// but just as true for every other call this UI makes). The server
// (internal/uiapi) already writes its refusal messages in plain English
// (see ApproveMachine/RejectMachine, ADR 0018); this only adds a short,
// code-specific headline above that message so the kind of problem is
// clear at a glance before reading the detail.
export function describeError(err: unknown): { title: string; detail: string } {
  if (err instanceof ConnectError) {
    switch (err.code) {
      case Code.FailedPrecondition:
        return { title: 'Not allowed right now', detail: err.message }
      case Code.InvalidArgument:
        return { title: 'Missing or invalid input', detail: err.message }
      case Code.NotFound:
        return { title: 'Not found', detail: err.message }
      case Code.Unavailable:
        return {
          title: 'Backend unavailable',
          detail: 'The UI backend is restarting or draining. Reconnecting automatically; try the action again in a moment.',
        }
      default:
        return { title: 'Request failed', detail: err.message }
    }
  }
  return { title: 'Request failed', detail: err instanceof Error ? err.message : String(err) }
}
