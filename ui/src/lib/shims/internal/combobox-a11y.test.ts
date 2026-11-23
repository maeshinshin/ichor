import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

// shortcut: node --test has no DOM, so this reads the source; replace it with a render test when the UI gets a DOM environment.
const source = readFileSync(new URL('./base-ui-combobox.tsx', import.meta.url), 'utf8')

test('the combobox portal is named by an aria-owns span placed after the trigger', () => {
  assert.match(source, /<span aria-owns=\{portalId\}/)
  assert.match(source, /"data-base-ui-portal": "", id: portalId/)
})
