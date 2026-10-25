import { cloneElement, type ComponentChildren, type VNode } from "preact"
import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, useState } from "preact/hooks"
import { useComposedRefs } from "./compose-refs"
import { getElementRef } from "./element"

type State = "mounted" | "unmountSuspended" | "unmounted"
type Event = "MOUNT" | "UNMOUNT" | "ANIMATION_OUT" | "ANIMATION_END"

const MACHINE: Record<State, Partial<Record<Event, State>>> = {
  mounted: { UNMOUNT: "unmounted", ANIMATION_OUT: "unmountSuspended" },
  unmountSuspended: { MOUNT: "mounted", ANIMATION_END: "unmounted" },
  unmounted: { MOUNT: "mounted" },
}

const getAnimationName = (styles: CSSStyleDeclaration | null) => styles?.animationName || "none"

function usePresence(present: boolean) {
  const [node, setNode] = useState<HTMLElement | null>(null)
  const stylesRef = useRef<CSSStyleDeclaration | null>(null)
  const prevPresentRef = useRef(present)
  const prevAnimationNameRef = useRef("none")
  const [state, send] = useReducer((state: State, event: Event) => MACHINE[state][event] ?? state, present ? "mounted" : "unmounted")

  useEffect(() => {
    prevAnimationNameRef.current = state === "mounted" ? getAnimationName(stylesRef.current) : "none"
  }, [state])

  useLayoutEffect(() => {
    const styles = stylesRef.current
    const wasPresent = prevPresentRef.current
    if (wasPresent !== present) {
      const prevAnimationName = prevAnimationNameRef.current
      const currentAnimationName = getAnimationName(styles)
      if (present) send("MOUNT")
      else if (currentAnimationName === "none" || styles?.display === "none") send("UNMOUNT")
      else if (wasPresent && prevAnimationName !== currentAnimationName) send("ANIMATION_OUT")
      else send("UNMOUNT")
      prevPresentRef.current = present
    }
  }, [present])

  useLayoutEffect(() => {
    if (!node) {
      send("ANIMATION_END")
      return
    }
    let timeoutId: number | undefined
    const ownerWindow = node.ownerDocument.defaultView ?? window
    const handleAnimationEnd = (event: AnimationEvent) => {
      const isCurrentAnimation = getAnimationName(stylesRef.current).includes(CSS.escape(event.animationName))
      if (event.target === node && isCurrentAnimation) {
        send("ANIMATION_END")
        if (!prevPresentRef.current) {
          // Keep the final keyframe until the node is removed to avoid a flash of the unstyled element
          const currentFillMode = node.style.animationFillMode
          node.style.animationFillMode = "forwards"
          timeoutId = ownerWindow.setTimeout(() => {
            if (node.style.animationFillMode === "forwards") node.style.animationFillMode = currentFillMode
          })
        }
      }
    }
    const handleAnimationStart = (event: AnimationEvent) => {
      if (event.target === node) prevAnimationNameRef.current = getAnimationName(stylesRef.current)
    }
    node.addEventListener("animationstart", handleAnimationStart)
    node.addEventListener("animationcancel", handleAnimationEnd)
    node.addEventListener("animationend", handleAnimationEnd)
    return () => {
      ownerWindow.clearTimeout(timeoutId)
      node.removeEventListener("animationstart", handleAnimationStart)
      node.removeEventListener("animationcancel", handleAnimationEnd)
      node.removeEventListener("animationend", handleAnimationEnd)
    }
  }, [node])

  return {
    isPresent: state === "mounted" || state === "unmountSuspended",
    ref: useCallback((node: HTMLElement | null) => {
      stylesRef.current = node ? getComputedStyle(node) : null
      setNode(node)
    }, []),
  }
}

type PresenceProps = {
  present: boolean
  children: VNode<any> | ((props: { present: boolean }) => VNode<any>)
}

function Presence({ present, children }: PresenceProps): ComponentChildren {
  const presence = usePresence(present)
  const child = typeof children === "function" ? children({ present: presence.isPresent }) : children
  const ref = useComposedRefs(presence.ref, getElementRef(child))
  const forceMount = typeof children === "function"
  return forceMount || presence.isPresent ? cloneElement(child, { ref }) : null
}

export { Presence, usePresence }
