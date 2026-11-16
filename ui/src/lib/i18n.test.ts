import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import ts from 'typescript'
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

// The UI's own source, test files left out, as [path under src, text].
const srcRoot = new URL('../', import.meta.url)
const sources = (readdirSync(srcRoot, { recursive: true }) as string[])
  .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f))
  .map((f) => [f, readFileSync(new URL(f, srcRoot), 'utf8')] as const)

test('the overview node count is read from overview.nodesReady, and the table defines that key', () => {
  assert.equal(typeof en['overview.nodesReady'], 'string')
  const overview = sources.find(([f]) => f === 'pages/Overview.tsx')?.[1] ?? ''
  assert.match(overview, /\bt\('overview\.nodesReady'/)
})

test('every literal t(\'key\') call in the UI names a key the table defines', () => {
  // This duplicates the Key type for literal calls and exists for when that type stops constraining them (Key widened to string).
  // A key built from a template string (ops.start.${kind}) or carried as data (a label field) is checked by tsc, whose t(key: Key) needs every member of the argument's type to be a key; the next test closes the one way round that.
  const calls = sources.flatMap(([, text]) => [...text.matchAll(/\bt\('([^']+)'/g)].map((m) => m[1]))
  assert.notEqual(calls.length, 0)
  assert.deepEqual([...new Set(calls)].filter((k) => !keys.includes(k as Key)), [])
})

test('no t(...) call passes its key through a type assertion, so tsc checks every key the UI looks up', () => {
  // shortcut: syntactic, so a key typed any still passes; the lint rule against explicit any covers the usual way to get one, so add a type-checker pass if one appears.
  let calls = 0
  const casts = sources.flatMap(([f, text]) => {
    const sf = ts.createSourceFile(f, text, ts.ScriptTarget.Latest, true, f.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
    const found: string[] = []
    const visit = (n: ts.Node) => {
      if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 't' && n.arguments.length > 0) {
        calls++
        let key = n.arguments[0]
        while (ts.isParenthesizedExpression(key)) key = key.expression
        if (ts.isAsExpression(key) || ts.isTypeAssertionExpression(key)) found.push(`${f}:${sf.getLineAndCharacterOfPosition(n.getStart()).line + 1}`)
      }
      ts.forEachChild(n, visit)
    }
    visit(sf)
    return found
  })
  assert.notEqual(calls, 0)
  assert.deepEqual(casts, [])
})
