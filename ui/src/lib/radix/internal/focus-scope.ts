import { useLayoutEffect, useRef } from "preact/hooks"

type Options = {
  // Wraps Tab and Shift+Tab around the edges of the scope
  loop?: boolean
  onMountAutoFocus?(event: Event): void
  onUnmountAutoFocus?(event: Event): void
}

function focus(element: HTMLElement | null | undefined, select = false) {
  if (!element?.focus) return
  const previouslyFocused = document.activeElement
  // Prevent scrolling to keep the transition calm
  element.focus({ preventScroll: true })
  if (select && element !== previouslyFocused && element instanceof HTMLInputElement) element.select()
}

function focusFirst(candidates: HTMLElement[]) {
  const previouslyFocused = document.activeElement
  for (const candidate of candidates) {
    focus(candidate, true)
    if (document.activeElement !== previouslyFocused) return
  }
}

// `tabIndex` reflects what the browser considers tabbable; positive tab indexes are not ordered
export function getTabbableCandidates(container: HTMLElement) {
  const nodes: HTMLElement[] = []
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_ELEMENT, {
    acceptNode: (node: any) =>
      node.disabled || node.hidden || (node.tagName === "INPUT" && node.type === "hidden") || node.tabIndex < 0
        ? NodeFilter.FILTER_SKIP
        : NodeFilter.FILTER_ACCEPT,
  })
  while (walker.nextNode()) nodes.push(walker.currentNode as HTMLElement)
  return nodes
}

function isHidden(node: HTMLElement | null, upTo: HTMLElement) {
  if (node && getComputedStyle(node).visibility === "hidden") return true
  for (; node && node !== upTo; node = node.parentElement) if (getComputedStyle(node).display === "none") return true
  return false
}

export function useFocusScope(node: HTMLElement | null, options: Options) {
  const latest = useRef(options)
  latest.current = options

  useLayoutEffect(() => {
    if (!node) return
    const previouslyFocused = document.activeElement as HTMLElement | null

    if (!node.contains(previouslyFocused)) {
      const event = new Event("focusScope.autoFocusOnMount", { cancelable: true })
      latest.current.onMountAutoFocus?.(event)
      if (!event.defaultPrevented) {
        focusFirst(getTabbableCandidates(node).filter((candidate) => candidate.tagName !== "A"))
        if (document.activeElement === previouslyFocused) focus(node)
      }
    }

    const onKeyDown = (event: KeyboardEvent) => {
      if (!latest.current.loop || event.key !== "Tab" || event.altKey || event.ctrlKey || event.metaKey) return
      const candidates = getTabbableCandidates(node).filter((candidate) => !isHidden(candidate, node))
      const first = candidates[0]
      const last = candidates[candidates.length - 1]
      if (!first || !last) {
        if (document.activeElement === node) event.preventDefault()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        focus(first, true)
      } else if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        focus(last, true)
      }
    }
    node.addEventListener("keydown", onKeyDown)

    return () => {
      node.removeEventListener("keydown", onKeyDown)
      // Deferred so the removal of the scope settles before focus moves
      setTimeout(() => {
        const event = new Event("focusScope.autoFocusOnUnmount", { cancelable: true })
        latest.current.onUnmountAutoFocus?.(event)
        if (!event.defaultPrevented) focus(previouslyFocused ?? document.body, true)
      })
    }
  }, [node])
}
