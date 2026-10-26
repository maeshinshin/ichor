// The pull request URL comes from a Git host's API response, relayed by the
// backend. Only an http(s) URL is ever linked: anything else (a javascript:
// URL, say) is shown as text, never as a href.
export function pullRequestHref(url: string): string | null {
  try {
    const u = new URL(url)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : null
  } catch {
    return null
  }
}
