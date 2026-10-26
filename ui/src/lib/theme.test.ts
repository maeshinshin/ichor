import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chooseTheme } from './theme.ts'

test('a saved choice wins over the system preference', () => {
  assert.equal(chooseTheme('light', true), 'light')
  assert.equal(chooseTheme('dark', false), 'dark')
})

test('without a saved choice the system preference decides', () => {
  assert.equal(chooseTheme(null, true), 'dark')
  assert.equal(chooseTheme(null, false), 'light')
  assert.equal(chooseTheme('sepia', true), 'dark')
})
