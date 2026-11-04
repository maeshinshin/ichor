// The CAs a rotation can replace (docs/adr/0120). Pure: no DOM, no preact, so it runs under `node --test`.

// The names are the ones the cluster's certificate expiry list and the operation's spec use.
export const caTargetChoices = ['talos', 'kubernetes', 'etcd', 'kubernetes-aggregator'] as const
export type CATarget = (typeof caTargetChoices)[number]

// What a rotation replaces when nothing else is chosen.
export const defaultCATargets: CATarget[] = ['talos', 'kubernetes']

// toggleCATarget returns the selection with name ticked or unticked, in the order of the choices.
export function toggleCATarget(selected: readonly string[], name: CATarget, on: boolean): CATarget[] {
  return caTargetChoices.filter((c) => (c === name ? on : selected.includes(c)))
}

// caNeedsCredentials says whether a rotation of these CAs changes something an operator's kubeconfig or
// talosconfig depends on, which is what makes it wait for a credential hand-off (docs/adr/0091). An empty
// list is what an operation without a choice carries: the default pair.
export function caNeedsCredentials(targets: readonly string[]): boolean {
  return targets.length === 0 || targets.includes('talos') || targets.includes('kubernetes')
}
