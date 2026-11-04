import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  caStageProgress,
  keyStageProgress,
  confirmMatches,
  kubernetesStageProgress,
  kindNeedsTypedName,
  operationState,
  pauseAction,
  planCounts,
  planSteps,
  pollIntervalMs,
  progressCounts,
  targetError,
} from './operations.ts'

test('a pause with a failure reason is a failure, without one it is the operator', () => {
  assert.equal(operationState('Paused', 'boom'), 'failed')
  assert.equal(operationState('Paused', ''), 'paused')
  assert.equal(operationState('Completed', ''), 'done')
  assert.equal(operationState('ShutDown', ''), 'shutdown')
  assert.equal(operationState('InProgress', ''), 'running')
  assert.equal(operationState('', ''), 'pending')
  assert.equal(operationState('Pending', ''), 'pending')
})

test('a failed or finished operation is not polled again, a running one is', () => {
  assert.equal(pollIntervalMs('failed'), null)
  assert.equal(pollIntervalMs('done'), null)
  assert.equal(pollIntervalMs('running'), 3000)
  assert.ok((pollIntervalMs('paused') ?? 0) > 3000)
})

test('machines are counted by phase and an unknown phase is neither', () => {
  assert.deepEqual(progressCounts([{ phase: 'Completed' }, { phase: 'InProgress' }, { phase: 'Completed' }, { phase: 'x' }]), {
    done: 2,
    running: 1,
  })
  assert.deepEqual(progressCounts([]), { done: 0, running: 0 })
})

test('resume is offered while paused by a person or after a failed step; nothing once over', () => {
  assert.equal(pauseAction(false, 'running'), 'pause')
  assert.equal(pauseAction(true, 'paused'), 'resume')
  assert.equal(pauseAction(true, 'failed'), 'resume')
  assert.equal(pauseAction(false, 'failed'), 'resume')
  assert.equal(pauseAction(false, 'done'), null)
  assert.equal(pauseAction(false, 'shutdown'), null)
})

test('plan steps keep their order and group machines that go together', () => {
  const ms = [
    { name: 'cp1', role: 'controlplane', step: 1 },
    { name: 'w1', role: 'worker', step: 2 },
    { name: 'w2', role: 'worker', step: 2 },
    { name: 'w3', role: 'worker', step: 3 },
  ]
  assert.deepEqual(planSteps(ms), [
    { step: 1, role: 'controlplane', names: ['cp1'] },
    { step: 2, role: 'worker', names: ['w1', 'w2'] },
    { step: 3, role: 'worker', names: ['w3'] },
  ])
  assert.deepEqual(planCounts(ms), { controlPlanes: 1, workers: 3 })
  assert.deepEqual(planSteps([]), [])
})

test('targets: versions for the upgrades, a mode for kube-proxy, none for a shutdown', () => {
  assert.equal(targetError('talos', 'v1.14.4'), null)
  assert.equal(targetError('kubernetes', '1.36.1'), null)
  assert.equal(targetError('ichor', 'v0.3.0-rc.1'), null)
  assert.equal(targetError('talos', ''), 'required')
  assert.equal(targetError('talos', 'latest'), 'invalid')
  assert.equal(targetError('cni', 'replaced'), null)
  assert.equal(targetError('cni', 'enabled'), null)
  assert.equal(targetError('cni', 'off'), 'invalid')
  assert.equal(targetError('cni', ' '), 'required')
  assert.equal(targetError('shutdown', ''), null)
})

test('the shutdown gate needs the exact cluster name', () => {
  assert.equal(confirmMatches('prod', 'prod'), true)
  assert.equal(confirmMatches(' prod ', 'prod'), true)
  assert.equal(confirmMatches('pro', 'prod'), false)
  assert.equal(confirmMatches('', ''), false)
})

test('a CA rotation takes no target and needs the cluster name typed, as a shutdown does', () => {
  assert.equal(targetError('ca-rotation', ''), null)
  assert.equal(kindNeedsTypedName('ca-rotation'), true)
  assert.equal(kindNeedsTypedName('shutdown'), true)
  assert.equal(kindNeedsTypedName('talos'), false)
})

test('a config rollout takes no target and needs the cluster name typed, because every machine in it reboots', () => {
  assert.equal(targetError('config', ''), null)
  assert.equal(kindNeedsTypedName('config'), true)
})

test('the CA rotation stage is a position in the stages; unknown or empty is not a position', () => {
  assert.deepEqual(caStageProgress('Accepting'), { name: 'Accepting', index: 1, total: 6 })
  assert.deepEqual(caStageProgress('AwaitingCredentials'), { name: 'AwaitingCredentials', index: 2, total: 6 })
  assert.deepEqual(caStageProgress('Dropping'), { name: 'Dropping', index: 4, total: 6 })
  assert.deepEqual(caStageProgress('Done'), { name: 'Done', index: 6, total: 6 })
  assert.equal(caStageProgress(''), null)
  assert.deepEqual(keyStageProgress('Reencrypting'), { name: 'Reencrypting', index: 3, total: 7 })
  assert.deepEqual(keyStageProgress('Done'), { name: 'Done', index: 7, total: 7 })
  assert.equal(keyStageProgress(''), null)
  assert.equal(targetError('key-rotation', ''), null)
  assert.equal(kindNeedsTypedName('key-rotation'), true)
  assert.equal(caStageProgress('Nonsense'), null)
})

test('the Kubernetes upgrade stage is a position in the four stages; unknown or empty is not a position', () => {
  assert.deepEqual(kubernetesStageProgress('APIServers'), { name: 'APIServers', index: 1, total: 4 })
  assert.deepEqual(kubernetesStageProgress('ControlPlaneComponents'), { name: 'ControlPlaneComponents', index: 2, total: 4 })
  assert.deepEqual(kubernetesStageProgress('KubeProxy'), { name: 'KubeProxy', index: 3, total: 4 })
  assert.deepEqual(kubernetesStageProgress('Kubelets'), { name: 'Kubelets', index: 4, total: 4 })
  assert.equal(kubernetesStageProgress(''), null)
  assert.equal(kubernetesStageProgress('Accepting'), null)
})
