export type ConditionTone = 'good' | 'bad' | 'neutral'

// Whether a condition being True is the healthy state. Kubernetes conditions
// are not uniform: "Ready" True is good, "Transitioning" True is just a fact
// about the cluster. Only the types that read as health get a good/bad tone;
// everything else is neutral so a False never looks like a failure by default.
const healthTypes = /(Ready|Available|Healthy|Synced|Approved|Present)$/

// CiliumUpgradeAvailable merely ends in a health word: "an upgrade is available" is not health.
export function conditionTone(type: string, status: string): ConditionTone {
  if (type === 'CiliumUpgradeAvailable') return 'neutral'
  if (!healthTypes.test(type)) return 'neutral'
  if (status === 'True') return 'good'
  if (status === 'False') return 'bad'
  return 'neutral'
}
