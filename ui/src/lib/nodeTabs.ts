// Tab ids of the node page and the ?tab= query value that selects one.
export const nodeTabs = ['summary', 'storage', 'talos', 'kubernetes', 'config', 'logs'] as const
export type NodeTab = (typeof nodeTabs)[number]

// parseNodeTab reads the tab out of a location.search string. An unknown or
// missing value falls back to the summary.
export function parseNodeTab(search: string): NodeTab {
  const v = new URLSearchParams(search).get('tab')
  return nodeTabs.find((id) => id === v) ?? 'summary'
}

// nodeTabSearch returns the query string for a tab, keeping other params; the
// default tab leaves the URL clean.
export function nodeTabSearch(search: string, tab: NodeTab): string {
  const p = new URLSearchParams(search)
  if (tab === 'summary') p.delete('tab')
  else p.set('tab', tab)
  const s = p.toString()
  return s ? `?${s}` : ''
}
