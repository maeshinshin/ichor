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

// readyLabel is what a TalosMachine Ready condition shows: its reason, else its status. That Ready is
// the controller's convergence state, not the kubelet's (kubectl's Ready), so the reason is the part
// that says what is not ready; a node waiting for an API server restart is Ready in kubectl.
export function readyLabel(c: { status: string; reason?: string }): string {
  return c.reason || c.status
}
