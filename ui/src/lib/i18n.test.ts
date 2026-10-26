import assert from 'node:assert/strict'
import { test } from 'node:test'
import { timestampFromDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { enumLabel } from './enum.ts'
import { formatBytes, formatDate, formatTimestamp } from './format.ts'
import { detectLang, en, enumName, ja, translate, type Key } from './i18n.ts'
import { getLang, setLang, storageKey, t } from './lang.ts'

const keys = Object.keys(en) as Key[]
const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()

test('ja has exactly the keys en has, checked at run time as well as by the compiler', () => {
  assert.deepEqual(Object.keys(ja).sort(), [...keys].sort())
})

test('no entry is empty, and ja and en fill in the same {placeholders}', () => {
  for (const k of keys) {
    assert.notEqual(en[k], '', k)
    assert.notEqual(ja[k], '', k)
    assert.deepEqual(placeholders(ja[k]), placeholders(en[k]), k)
  }
})

test('every enum key is looked up by its proto value name', () => {
  assert.equal(enumName('en', 'HANDED_OVER'), 'Handed Over')
  assert.equal(enumName('ja', 'HANDED_OVER'), ja['enum.HANDED_OVER'])
  assert.equal(enumName('ja', 'NEW_FUTURE_VALUE'), undefined)
})

test('detectLang: a saved choice wins, then the browser, then English', () => {
  assert.equal(detectLang('ja', ['en-US']), 'ja')
  assert.equal(detectLang('en', ['ja']), 'en')
  assert.equal(detectLang(null, ['ja-JP', 'en']), 'ja')
  assert.equal(detectLang(null, ['fr-FR', 'ja']), 'ja')
  assert.equal(detectLang(null, ['en-GB']), 'en')
  assert.equal(detectLang(null, ['fr-FR']), 'en')
  assert.equal(detectLang(null, undefined), 'en')
  assert.equal(detectLang('de', []), 'en')
  assert.equal(detectLang('', ['JA']), 'ja')
})

test('translate fills placeholders and leaves an unknown one visible', () => {
  assert.equal(translate('en', 'machines.pending', { n: 3 }), 'Pending approval (3)')
  assert.equal(translate('ja', 'machines.pending', { n: 3 }), '承認待ち (3)')
  assert.equal(translate('en', 'machines.pending', {}), 'Pending approval ({n})')
  assert.equal(translate('en', 'machines.pending'), 'Pending approval ({n})')
})

test('setLang switches t() and enumLabel(), and an unknown enum value falls back to its title-cased name', () => {
  const values = { UNSPECIFIED: 0, HANDED_OVER: 4, BRAND_NEW: 9 }
  setLang('en')
  assert.equal(t('common.cancel'), 'Cancel')
  assert.equal(enumLabel(values, 4), 'Handed Over')
  assert.equal(enumLabel(values, 9), 'Brand New')
  assert.equal(enumLabel(values, 77), 'Unknown (77)')
  setLang('ja')
  assert.equal(getLang(), 'ja')
  assert.equal(t('common.cancel'), 'キャンセル')
  assert.equal(enumLabel(values, 4), ja['enum.HANDED_OVER'])
  assert.equal(enumLabel(values, 9), 'Brand New')
  setLang('en')
})

test('the choice is saved under one key when storage exists', () => {
  const store = new Map<string, string>()
  const real = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => store.set(k, v) },
  })
  try {
    setLang('en')
    setLang('ja')
    assert.equal(store.get(storageKey), 'ja')
  } finally {
    if (real) Object.defineProperty(globalThis, 'localStorage', real)
    else delete (globalThis as { localStorage?: unknown }).localStorage
    setLang('en')
  }
})

test('formatBytes uses binary units with the language\'s digits', () => {
  assert.equal(formatBytes(undefined, 'en'), '0 B')
  assert.equal(formatBytes(512, 'en'), '512 B')
  assert.equal(formatBytes(1536, 'en'), '1.5 KB')
  assert.equal(formatBytes(1536n, 'ja'), '1.5 KB')
  assert.equal(formatBytes(5 * 1024 ** 3, 'ja'), '5.0 GB')
  assert.equal(formatBytes(1234 * 1024 ** 4, 'en'), '1,234.0 TB')
  assert.equal(formatBytes(1234 * 1024 ** 4, 'ja'), '1,234.0 TB')
  assert.equal(formatBytes(2 ** 53, 'en'), '8,192.0 TB')
  assert.equal(formatBytes(Number.NaN, 'en'), '0 B')
})

test('formatTimestamp follows the language and shows a dash for none', () => {
  const d = new Date(2026, 9, 26, 15, 4, 5)
  const ts: Timestamp = timestampFromDate(d)
  assert.equal(formatTimestamp(undefined, 'en'), '-')
  assert.equal(formatTimestamp(ts, 'en'), new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'medium' }).format(d))
  assert.equal(formatDate(d, 'ja'), '2026/10/26 15:04:05')
  assert.match(formatDate(d, 'en'), /Oct/)
  assert.notEqual(formatDate(d, 'en'), formatDate(d, 'ja'))
})
