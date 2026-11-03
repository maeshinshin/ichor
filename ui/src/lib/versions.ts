// What the Versions card shows about a cluster against the release set of its Ichor (ADR 0113).
// Pure: no DOM, no preact, so it runs under `node --test` (docs/adr/0045).

import { t, type Key } from './i18n.ts'
import type { Tone } from './status.ts'

// stateTone draws a component's state word: up to date is good, behind is a warning, the rest is neutral.
export function stateTone(state: string): Tone {
  switch (state) {
    case 'up-to-date':
      return 'good'
    case 'behind':
      return 'warn'
    default:
      return 'neutral'
  }
}

// stateKey names a component's state word in English. A state this UI does not know reads as unknown.
export function stateKey(state: string): Key {
  switch (state) {
    case 'up-to-date':
      return 'versions.state.up-to-date'
    case 'behind':
      return 'versions.state.behind'
    case 'ahead':
      return 'versions.state.ahead'
    default:
      return 'versions.state.unknown'
  }
}

// componentOf is the cluster's comparison for one component (talos, version-contract, kubernetes, cilium).
export function componentOf<T extends { name: string }>(list: readonly T[], name: string): T | undefined {
  return list.find((c) => c.name === name)
}

// versionPair shows what a component runs and where it is going: "v1.12.4 → v1.14.3". A missing side is a dash.
export function versionPair(current: string, target: string): string {
  const from = current || '-'
  return target ? `${from} → ${target}` : from
}

// StepInput is one planned step of the rollout; ReleaseInput is the release set it brings the cluster to.
export type StepInput = { step: string; from: string; to: string }
export type ReleaseInput = { versionContract: string; ciliumChartVersion: string }

// stepLabel is one line of the confirmation: what a step does, in the words the dialog shows.
export function stepLabel(step: StepInput, release?: ReleaseInput): string {
  const from = step.from || '-'
  switch (step.step) {
    case 'talos':
      return t('versions.step.talos', { from, to: step.to || '-', contract: release?.versionContract || '-' })
    case 'config':
      return t('versions.step.config')
    case 'kubernetes':
      return t('versions.step.kubernetes', { from, to: step.to || '-' })
    case 'cilium':
      // The server leaves the Cilium target empty on the step; the release set names it.
      return t('versions.step.cilium', { from, to: step.to || release?.ciliumChartVersion || '-' })
    default:
      return step.step
  }
}

// upgradeLabel is the one button's label: the Ichor version the cluster is brought to.
export function upgradeLabel(ichorVersion: string | null): string {
  return ichorVersion ? t('versions.bring', { version: ichorVersion }) : t('versions.bringUnknown')
}
