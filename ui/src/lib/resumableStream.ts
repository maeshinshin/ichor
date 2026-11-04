import { Code, ConnectError } from '@connectrpc/connect'

// architecture.md ("CLI and UI") requires streams that "resume from a
// token" and survive a backend drain's server ends a draining
// StreamNodeLogs call with CodeUnavailable and asks the client to
// reconnect with its latest resume token. This is that reconnect loop,
// generic over the one streaming RPC this phase has (StreamNodeLogs) so it
// can be unit-tested against a fake call instead of a real server.
//
// `call` opens one connection starting from `resumeToken` and returns the
// async iterable of messages; `getResumeToken` reads the token to resume
// from off each message. A clean stream end (the iterable finishes without
// throwing) ends the generator; CodeUnavailable reconnects from the latest
// token seen so far; any other error propagates to the caller.
//
// Reconnects back off (500 ms doubling to 10 s) and give up after
// `maxAttempts` in a row with no message in between: an unreachable node
// answers Unavailable every time, and an immediate retry loop would hammer
// the backend and the page forever. `signal` ends the loop.
export interface ResumeOptions {
  maxAttempts?: number
  // reauth runs when the server ends the stream because the ID token it was opened with expired
  // (Unauthenticated): true means a new token is stored, so the stream reconnects. It counts
  // against maxAttempts like a reconnect.
  reauth?: () => Promise<boolean>
  signal?: AbortSignal
  sleep?: (ms: number) => Promise<void>
}

export async function* streamWithResume<T>(
  call: (resumeToken: string) => AsyncIterable<T>,
  getResumeToken: (message: T) => string,
  initialResumeToken = '',
  { maxAttempts = 8, reauth, signal, sleep = defaultSleep }: ResumeOptions = {},
): AsyncGenerator<T, void, unknown> {
  let token = initialResumeToken
  let attempts = 0
  for (;;) {
    try {
      for await (const message of call(token)) {
        attempts = 0
        const next = getResumeToken(message)
        if (next) token = next
        yield message
      }
      return
    } catch (err) {
      if (signal?.aborted) return
      if (err instanceof ConnectError && err.code === Code.Unavailable && attempts < maxAttempts) {
        await sleep(Math.min(500 * 2 ** attempts, 10_000))
        attempts++
        if (signal?.aborted) return
        continue
      }
      if (err instanceof ConnectError && err.code === Code.Unauthenticated && reauth && attempts < maxAttempts && (await reauth())) {
        attempts++
        if (signal?.aborted) return
        continue
      }
      throw err
    }
  }
}

const defaultSleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))
