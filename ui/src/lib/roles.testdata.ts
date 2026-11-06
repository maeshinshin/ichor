// What the built-in roles may do (config/rbac/ichor_*_role.yaml), as GetMyPermissions answers it. Shared by the tests.
import type { ActionPermissions } from './actions.ts'

export const viewer: ActionPermissions = {
  listUsers: false,
  createOperations: false,
  startCaRotation: false,
  startKeyRotation: false,
  startShutdown: false,
  startEtcdRestore: false,
  updateIchorVersion: false,
  updateBackups: false,
  clusterUpdate: false,
  talosconfigReader: false,
  startNodeReset: false,
  startNodeShutdown: false,
  overrideMachines: false,
  updateMachines: false,
  deleteMachines: false,
  approveMachines: false,
  createPatches: false,
  updatePatches: false,
  deletePatches: false,
}

export const operator: ActionPermissions = {
  ...viewer,
  createOperations: true,
  updateBackups: true,
  clusterUpdate: true,
  talosconfigReader: true,
  updateMachines: true,
  approveMachines: true,
  createPatches: true,
  updatePatches: true,
  deletePatches: true,
}

export const admin: ActionPermissions = {
  ...operator,
  listUsers: true,
  startCaRotation: true,
  startKeyRotation: true,
  startShutdown: true,
  startEtcdRestore: true,
  updateIchorVersion: true,
  startNodeReset: true,
  startNodeShutdown: true,
  overrideMachines: true,
  deleteMachines: true,
}
