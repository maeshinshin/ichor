import assert from 'node:assert/strict'
import { test } from 'node:test'
import { machineDisplayName } from './machineName.ts'

const ref = { name: 'uuid-1' } as never

test('machineDisplayName prefers the hostname, then the node name, then the ref name', () => {
  assert.equal(machineDisplayName({ hostname: 'h', nodeName: 'n', ref }), 'h')
  assert.equal(machineDisplayName({ hostname: '', nodeName: 'n', ref }), 'n')
  assert.equal(machineDisplayName({ hostname: '', nodeName: '', ref }), 'uuid-1')
  assert.equal(machineDisplayName({ hostname: '', nodeName: '', ref: undefined }), '-')
})
