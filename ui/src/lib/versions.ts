// What the Versions card shows about a cluster's components. The Behind and Ahead
// words are kept for the Cilium row only; Talos, the contract and Kubernetes show what they run.
// Pure: no DOM, no preact, so it runs under `node --test`.

import type { Key } from './i18n.ts'
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

