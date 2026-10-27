// A minimal client-side router: no dependency earns its keep for a handful
// of placeholder routes. History API + a popstate listener is enough.
import type { JSX } from 'preact'
import { useEffect, useState } from 'preact/hooks'

export function usePath(): string {
  const [path, setPath] = useState(window.location.pathname)

  useEffect(() => {
    const onPopState = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  return path
}

// replace swaps the current history entry (a redirect from an old URL), so Back does not loop.
export function replace(to: string) {
  window.history.replaceState({}, '', to)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export function navigate(to: string) {
  if (to !== window.location.pathname) {
    window.history.pushState({}, '', to)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }
}

// matchRoute supports one thing beyond the phase-5 skeleton's exact-path
// table: a ":param" segment (cluster/node detail pages need a namespace and
// name out of the URL). Still no dependency - it's a split and a loop.
export function matchRoute(pattern: string, path: string): Record<string, string> | null {
  const patternParts = pattern.split('/').filter(Boolean)
  const pathParts = path.split('/').filter(Boolean)
  if (patternParts.length !== pathParts.length) return null

  const params: Record<string, string> = {}
  for (let i = 0; i < patternParts.length; i++) {
    const part = patternParts[i]
    if (part.startsWith(':')) {
      params[part.slice(1)] = decodeURIComponent(pathParts[i])
    } else if (part !== pathParts[i]) {
      return null
    }
  }
  return params
}

type LinkProps = Omit<JSX.HTMLAttributes<HTMLAnchorElement>, 'href'> & {
  to: string
}

export function Link({ to, onClick, ...props }: LinkProps) {
  return (
    <a
      href={to}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
          return
        }
        event.preventDefault()
        navigate(to)
        if (onClick) onClick(event)
      }}
      {...props}
    />
  )
}
