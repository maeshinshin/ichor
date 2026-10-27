// The top bar's health badge: Upgrading while a rollout runs, Ready when the cluster's Ready
// condition is True, Degraded otherwise.
export type Health = 'ready' | 'degraded' | 'upgrading'

export function clusterHealth(c: { conditions: readonly { type: string; status: string }[]; rollout?: unknown }): Health {
  if (c.rollout) return 'upgrading'
  return c.conditions.find((x) => x.type === 'Ready')?.status === 'True' ? 'ready' : 'degraded'
}
