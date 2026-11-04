import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canContinue, canDownload, credentialLifetimes, credentialsPanel, lifetimeSeconds, readerLifetimeSeconds, readerLifetimes } from './credentials.ts'

const ca = true

test('the gate is shown only while a ca-rotation waits for credentials', () => {
  assert.equal(credentialsPanel(ca, 'AwaitingCredentials', false), 'gate')
  assert.equal(credentialsPanel(ca, 'Accepting', false), 'none')
  assert.equal(credentialsPanel(ca, '', false), 'none')
  assert.equal(credentialsPanel(false, 'AwaitingCredentials', false), 'none')
  assert.equal(credentialsPanel(ca, 'AwaitingCredentials', true), 'none')
})

test('after the gate the download stays offered until the rotation is done', () => {
  for (const stage of ['Issuing', 'Dropping', 'Finalizing']) assert.equal(credentialsPanel(ca, stage, false), 'issued', stage)
  assert.equal(credentialsPanel(ca, 'Done', false), 'none')
  assert.equal(credentialsPanel(ca, 'Issuing', true), 'none')
})

test('continuing is offered at the gate only, and once', () => {
  assert.equal(canContinue('gate', false), true)
  assert.equal(canContinue('gate', true), false)
  assert.equal(canContinue('issued', false), false)
  assert.equal(canContinue('none', false), false)
})

test('the download is offered only to a caller who holds the verb', () => {
  assert.equal(canDownload('gate', true), true)
  assert.equal(canDownload('issued', true), true)
  assert.equal(canDownload('gate', false), false)
  assert.equal(canDownload('gate', undefined), false)
  assert.equal(canDownload('none', true), false)
})

test('the lifetime choices default to a year, the server maximum, and none is shorter than a day', () => {
  assert.equal(lifetimeSeconds('year'), 365 * 24 * 3600)
  assert.equal(lifetimeSeconds('nonsense'), 365 * 24 * 3600)
  assert.equal(credentialLifetimes[0].key, 'year')
  assert.ok(credentialLifetimes.every((l) => l.seconds >= 24 * 3600 && l.seconds <= 365 * 24 * 3600))
})

test('the reader talosconfig choices default to an hour and none is longer than the server maximum of twelve hours', () => {
  assert.equal(readerLifetimeSeconds('hour'), 3600)
  assert.equal(readerLifetimeSeconds('nonsense'), 3600)
  assert.equal(readerLifetimes[0].key, 'hour')
  assert.ok(readerLifetimes.every((l) => l.seconds >= 3600 && l.seconds <= 12 * 3600))
})
