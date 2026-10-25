import { Code, ConnectError } from '@connectrpc/connect'

// architecture.md ("CLI and UI") requires streams that "resume from a
// token" and survive a backend drain; ADR 0018's server ends a draining
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
export async function* streamWithResume<T>(
  call: (resumeToken: string) => AsyncIterable<T>,
  getResumeToken: (message: T) => string,
  initialResumeToken = '',
): AsyncGenerator<T, void, unknown> {
  let token = initialResumeToken
  for (;;) {
    try {
      for await (const message of call(token)) {
        const next = getResumeToken(message)
        if (next) token = next
        yield message
      }
      return
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unavailable) {
        continue
      }
      throw err
    }
  }
}
