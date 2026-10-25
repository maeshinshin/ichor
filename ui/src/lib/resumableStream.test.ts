import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { streamWithResume } from './resumableStream.ts'

interface FakeLine {
  message: string
  resumeToken: string
}

test('reconnects from the latest resume token after CodeUnavailable', async () => {
  const tokensRequested: string[] = []

  async function* fakeStreamNodeLogs(resumeToken: string): AsyncIterable<FakeLine> {
    tokensRequested.push(resumeToken)
    if (tokensRequested.length === 1) {
      yield { message: 'a', resumeToken: 't1' }
      yield { message: 'b', resumeToken: 't2' }
      // Mirrors the server draining mid-stream (ADR 0018): the client must
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
  function fails(): AsyncIterable<FakeLine> {
    return {
      [Symbol.asyncIterator]() {
        return { next: () => Promise.reject(new ConnectError('no permission', Code.PermissionDenied)) }
      },
    }
  }

  const iterator = streamWithResume(fails, (l) => l.resumeToken)
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
