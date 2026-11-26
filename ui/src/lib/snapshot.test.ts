import assert from 'node:assert/strict'
import { test } from 'node:test'
import { refusalOf, refusalWithin } from './snapshot.ts'

const after = <T>(ms: number, v: T) => new Promise<T>((r) => setTimeout(() => r(v), ms))

test('a refusal inside the window is returned and not reported late', async () => {
  const late: string[] = []
  assert.equal(await refusalWithin(after(5, 'denied'), 200, (r) => late.push(r)), 'denied')
  await after(20, 0)
  assert.deepEqual(late, [])
})

test('no refusal in the window means the download started', async () => {
  assert.equal(await refusalWithin(new Promise<string>(() => {}), 10, () => {}), '')
})

test('a refusal after the window still produces an error result', async () => {
  const late: string[] = []
  assert.equal(await refusalWithin(after(40, 'credential wait timed out'), 10, (r) => late.push(r)), '')
  await after(60, 0)
  assert.deepEqual(late, ['credential wait timed out'])
})

test('the about:blank load of a new frame is not a refusal; the page that follows it is', () => {
  const blank = { href: 'about:blank', text: '' }
  assert.equal(refusalOf([blank, { href: 'https://x/snapshot', text: 'forbidden' }]), 'forbidden')
  assert.equal(refusalOf([blank]), null)
  assert.equal(refusalOf([]), null)
})
