import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canApprove, isStrongIdentity } from './identity.ts'

// Approval is refused unless UUID, serial and a MAC are all present.
const full = { uuid: 'u', serial: 's', macAddresses: ['aa:bb'] }

test('isStrongIdentity is true only with uuid, serial and a MAC', () => {
  assert.equal(isStrongIdentity(full as never), true)
  assert.equal(isStrongIdentity({ ...full, uuid: '' } as never), false)
  assert.equal(isStrongIdentity({ ...full, serial: '' } as never), false)
  assert.equal(isStrongIdentity({ ...full, macAddresses: [] } as never), false)
  assert.equal(isStrongIdentity(undefined), false)
})

test('canApprove needs a strong identity and a chosen role', () => {
  assert.equal(canApprove(full as never, true), true)
  assert.equal(canApprove(full as never, false), false)
  assert.equal(canApprove({ ...full, serial: '' } as never, true), false)
  assert.equal(canApprove(undefined, true), false)
})
