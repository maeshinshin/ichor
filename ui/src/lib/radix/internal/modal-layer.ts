import type { RefObject } from "preact"
import { useLayoutEffect } from "preact/hooks"

const counts = { inert: new WeakMap<Element, number>(), "aria-hidden": new WeakMap<Element, number>() }
const VALUES = { inert: "", "aria-hidden": "true" }
let scrollLocks = 0
let restoreScroll = () => {}

// Radix hides the rest of the page with aria-hidden and traps focus; `inert` does both. Like the aria-hidden package it only marks
// siblings along the path from body to the layer and spares `[aria-live]` regions (a Toaster) and scripts: `inert` is inherited, so
// no ancestor of a spared node may carry it. Elements added after the layer opens (nested popovers) are not covered.
// A popup that leaves the page usable (a combobox) asks for "aria-hidden" alone.
// shortcut: an element that already has the attribute, aria-hidden="false" included, is left alone; handle "false" when a page needs it.
export function hideOthers(attribute: "inert" | "aria-hidden", ...keep: Array<Element | null | undefined>) {
  const marked = counts[attribute]
  const targets = [...keep.filter((node) => node != null), ...document.body.querySelectorAll("[aria-live]")]
  const path = new Set<Node>()
  for (const target of targets) for (let node: Node | null = target; node; node = node.parentNode) path.add(node)
  const hidden: Element[] = []
  const collect = (parent: Element) => {
    for (const child of Array.from(parent.children)) {
      if (path.has(child)) {
        if (!targets.includes(child)) collect(child)
      } else if (child.localName !== "script" && (marked.has(child) || !child.hasAttribute(attribute))) hidden.push(child)
    }
  }
  collect(document.body)
  for (const element of hidden) {
    marked.set(element, (marked.get(element) ?? 0) + 1)
    element.setAttribute(attribute, VALUES[attribute])
  }
  return () => {
    for (const element of hidden) {
      const count = marked.get(element)! - 1
      marked.set(element, count)
      if (count === 0) element.removeAttribute(attribute)
    }
  }
}

// shortcut: only `overflow: hidden` on body, no wheel/touch interception; add it if the page still scrolls behind a modal on iOS
function lockScroll() {
  if (scrollLocks++ === 0) {
    const { body, documentElement } = document
    const { overflow, paddingRight } = body.style
    const scrollbarWidth = window.innerWidth - documentElement.clientWidth
    body.style.overflow = "hidden"
    if (scrollbarWidth > 0) body.style.paddingRight = `${(parseFloat(getComputedStyle(body).paddingRight) || 0) + scrollbarWidth}px`
    restoreScroll = () => Object.assign(body.style, { overflow, paddingRight })
  }
  return () => {
    if (--scrollLocks === 0) restoreScroll()
  }
}

// For layers that block the page behind them: dialogs, and later modal menus and selects
export function useModalLayer(node: HTMLElement | null, overlay?: RefObject<Element | null>) {
  useLayoutEffect(() => {
    if (!node) return
    const show = hideOthers("inert", node, overlay?.current)
    const unlock = lockScroll()
    return () => {
      show()
      unlock()
    }
  }, [node])
}
