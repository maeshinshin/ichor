import assert from 'node:assert/strict'
import { test } from 'node:test'
import { timestampFromDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { enumLabel } from './enum.ts'
import { display, formatBytes, formatCores, formatDate, formatTimestamp, formatUsage } from './format.ts'
import { en, enumName, t, type Key } from './i18n.ts'

const keys = Object.keys(en) as Key[]

test('no entry is empty', () => {
  for (const k of keys) assert.notEqual(en[k], '', k)
})

test('typed-confirmation labels leave the name out; the name is shown beside them as selectable text', () => {
  for (const k of ['ops.dialog.typeName', 'node.action.typeName', 'node.action.typeNameHost'] as Key[]) {
    assert.doesNotMatch(en[k], /\{name\}/, k)
  }
})

test('every enum key is looked up by its proto value name', () => {
  assert.equal(enumName('HANDED_OVER'), 'Handed Over')
  assert.equal(enumName('NEW_FUTURE_VALUE'), undefined)
})

test('t fills placeholders and leaves an unknown one visible', () => {
  assert.equal(t('machines.pending', { n: 3 }), 'Pending approval (3)')
  assert.equal(t('machines.pending', {}), 'Pending approval ({n})')
  assert.equal(t('machines.pending'), 'Pending approval ({n})')
})

test('enumLabel uses the dictionary, and an unknown value falls back to its title-cased name', () => {
  const values = { UNSPECIFIED: 0, HANDED_OVER: 4, BRAND_NEW: 9 }
  assert.equal(t('common.cancel'), 'Cancel')
  assert.equal(enumLabel(values, 4), 'Handed Over')
  assert.equal(enumLabel(values, 9), 'Brand New')
  assert.equal(enumLabel(values, 77), 'Unknown (77)')
})

test('formatBytes uses binary units with English digits', () => {
  assert.equal(formatBytes(undefined), '0 B')
  assert.equal(formatBytes(512), '512 B')
  assert.equal(formatBytes(1536), '1.5 KB')
  assert.equal(formatBytes(1536n), '1.5 KB')
  assert.equal(formatBytes(1234 * 1024 ** 4), '1,234.0 TB')
  assert.equal(formatBytes(2 ** 53), '8,192.0 TB')
  assert.equal(formatBytes(Number.NaN), '0 B')
})

test('formatTimestamp is English and shows a dash for none', () => {
  const d = new Date(2026, 9, 26, 15, 4, 5)
  const ts: Timestamp = timestampFromDate(d)
  assert.equal(formatTimestamp(undefined), '-')
  assert.equal(formatTimestamp(ts), new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'medium' }).format(d))
  assert.match(formatDate(d), /Oct/)
})

test('display shows missing values and firmware stand-ins as a dash', () => {
  assert.equal(display(undefined), '-')
  assert.equal(display(''), '-')
  assert.equal(display('  '), '-')
  assert.equal(display('None'), '-')
  assert.equal(display('To Be Filled By O.E.M.'), '-')
  assert.equal(display('QEMU'), 'QEMU')
})

test('usage shows used of total, and a dash when the total or the sample is unknown', () => {
  assert.equal(formatUsage(1n << 30n, 4n << 30n), '1.0 GB / 4.0 GB (25%)')
  assert.equal(formatUsage(0n, 0n), '-')
  assert.equal(formatCores(1.234, true), '1.23')
  assert.equal(formatCores(0, false), '-')
  assert.equal(formatCores(-24.99, true), '-')
})
