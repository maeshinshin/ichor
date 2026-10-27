import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canContinue, canDownload, credentialsPanel } from './credentials.ts'

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
