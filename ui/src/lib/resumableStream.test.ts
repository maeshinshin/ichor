import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { streamWithResume } from './resumableStream.ts'

interface FakeLine {
  message: string
  resumeToken: string
}

// rejecting is a stream whose first read fails with err.
const rejecting = (err: Error): AsyncIterable<FakeLine> => ({ [Symbol.asyncIterator]: () => ({ next: () => Promise.reject(err) }) })

test('reconnects from the latest resume token after CodeUnavailable', async () => {
  const tokensRequested: string[] = []

  async function* fakeStreamNodeLogs(resumeToken: string): AsyncIterable<FakeLine> {
    tokensRequested.push(resumeToken)
    if (tokensRequested.length === 1) {
      yield { message: 'a', resumeToken: 't1' }
      yield { message: 'b', resumeToken: 't2' }
      // Mirrors the server draining mid-stream: the client must
      // reconnect with the latest token it saw, "t2", not restart from "".
      throw new ConnectError('draining, reconnect with your resume token', Code.Unavailable)
    }
    assert.equal(resumeToken, 't2')
    yield { message: 'c', resumeToken: 't3' }
  }

  const seen: string[] = []
  for await (const line of streamWithResume(fakeStreamNodeLogs, (l) => l.resumeToken)) {
    seen.push(line.message)
  }

  assert.deepEqual(seen, ['a', 'b', 'c'])
  assert.deepEqual(tokensRequested, ['', 't2'])
})

test('does not reconnect on an error other than CodeUnavailable', async () => {
  const iterator = streamWithResume(() => rejecting(new ConnectError('no permission', Code.PermissionDenied)), (l) => l.resumeToken)
  await assert.rejects(() => iterator.next(), /no permission/)
})

test('ends cleanly without reconnecting when the stream finishes normally', async () => {
  let calls = 0
  async function* once(): AsyncIterable<FakeLine> {
    calls++
    yield { message: 'only', resumeToken: 't1' }
  }

  const seen: string[] = []
  for await (const line of streamWithResume(once, (l) => l.resumeToken)) {
    seen.push(line.message)
  }

  assert.deepEqual(seen, ['only'])
  assert.equal(calls, 1)
})

test('backs off between reconnects and gives up after repeated failures with no message', async () => {
  let calls = 0
  const delays: number[] = []
  function alwaysUnavailable(): AsyncIterable<FakeLine> {
    calls++
    return rejecting(new ConnectError('node unreachable', Code.Unavailable))
  }

  const iterator = streamWithResume(alwaysUnavailable, (l) => l.resumeToken, '', {
    maxAttempts: 3,
    sleep: async (ms) => {
      delays.push(ms)
    },
  })
  await assert.rejects(() => iterator.next(), /node unreachable/)
  assert.equal(calls, 4, 'first call plus three reconnects')
  assert.equal(delays.length, 3)
  assert.ok(delays[1] > delays[0] && delays[2] > delays[1], 'delays grow')
})

test('a received message resets the reconnect budget', async () => {
  let calls = 0
  async function* flaky(): AsyncIterable<FakeLine> {
    calls++
    if (calls < 6) {
      yield { message: String(calls), resumeToken: `t${calls}` }
      throw new ConnectError('draining', Code.Unavailable)
    }
  }
  const seen: string[] = []
  for await (const l of streamWithResume(flaky, (x) => x.resumeToken, '', { maxAttempts: 2, sleep: async () => {} })) {
    seen.push(l.message)
  }
  assert.deepEqual(seen, ['1', '2', '3', '4', '5'])
})

test('stops without reconnecting once aborted', async () => {
  const controller = new AbortController()
  let calls = 0
  function unavailable(): AsyncIterable<FakeLine> {
    calls++
    return rejecting(new ConnectError('draining', Code.Unavailable))
  }
  const iterator = streamWithResume(unavailable, (l) => l.resumeToken, '', {
    signal: controller.signal,
    sleep: async () => controller.abort(),
  })
  const result = await iterator.next()
  assert.equal(result.done, true)
  assert.equal(calls, 1)
})

// The server ends a stream with Unauthenticated when the ID token it was opened with expires
// (internal/uiapi/auth.go); with a refresh token the page signs in silently and resumes.
test('an expired sign-in is refreshed and the stream resumes from the latest token', async () => {
  const requested: string[] = []
  async function* expiring(resumeToken: string): AsyncIterable<FakeLine> {
    requested.push(resumeToken)
    if (requested.length === 1) {
      yield { message: 'a', resumeToken: 't1' }
      throw new ConnectError('your sign-in expired; sign in again', Code.Unauthenticated)
    }
    yield { message: 'b', resumeToken: 't2' }
  }
  let reauths = 0
  const seen: string[] = []
  for await (const l of streamWithResume(expiring, (x) => x.resumeToken, '', { reauth: async () => ++reauths > 0 })) seen.push(l.message)
  assert.deepEqual(seen, ['a', 'b'])
  assert.deepEqual(requested, ['', 't1'])
  assert.equal(reauths, 1)
})

test('an expired sign-in that cannot be refreshed ends the stream with the error', async () => {
  const iterator = streamWithResume(() => rejecting(new ConnectError('expired', Code.Unauthenticated)), (l) => l.resumeToken, '', { reauth: async () => false })
  await assert.rejects(() => iterator.next(), /expired/)
  const plain = streamWithResume(() => rejecting(new ConnectError('expired', Code.Unauthenticated)), (l) => l.resumeToken)
  await assert.rejects(() => plain.next(), /expired/)
})
