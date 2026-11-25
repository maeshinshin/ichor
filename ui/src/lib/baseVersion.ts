// Pure helpers for writes that replace a whole object from a copy the page loaded. No import from "@/gen"
// or preact, so they run under `node --test`.
import { Code, ConnectError } from '@connectrpc/connect'

// withBase tags a replace request with the resourceVersion the copy was read at, so the server refuses it
// if someone else saved meanwhile.
export function withBase<T extends object>(req: T, resourceVersion: string): T & { baseResourceVersion: string } {
  return { ...req, baseResourceVersion: resourceVersion }
}

// isChanged is true when the server refused a save because the object changed since it was opened.
export function isChanged(err: unknown): boolean {
  return err instanceof ConnectError && err.code === Code.Aborted
}
