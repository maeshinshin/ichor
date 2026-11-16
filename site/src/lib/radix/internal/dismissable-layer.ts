import { useLayoutEffect, useRef } from "preact/hooks"

export type OutsideEvent<E extends Event> = CustomEvent<{ originalEvent: E }>

export type DismissableLayerOptions = {
  onEscapeKeyDown?(event: KeyboardEvent): void
  onPointerDownOutside?(event: OutsideEvent<PointerEvent>): void
  onFocusOutside?(event: OutsideEvent<FocusEvent>): void
  onInteractOutside?(event: OutsideEvent<PointerEvent | FocusEvent>): void
  onDismiss?(): void
}

// The props content components expose so callers can veto a dismissal
export type DismissableLayerProps = Omit<DismissableLayerOptions, "onDismiss">

// Open layers in creation order; only the last one reacts to Escape
const layers: HTMLElement[] = []

// How far a finger may drift and still be a tap
const TAP_SLOP = 10

// Preact events do not bubble through portals, so "inside" means the layer or any layer opened after it
export function useDismissableLayer(node: HTMLElement | null, options: DismissableLayerOptions) {
  const latest = useRef(options)
  latest.current = options

  useLayoutEffect(() => {
    if (!node) return
    layers.push(node)
    const isInside = (target: EventTarget | null) => layers.slice(layers.indexOf(node)).some((layer) => layer.contains(target as Node))

    const dismiss = (originalEvent: PointerEvent | FocusEvent, handler: "onPointerDownOutside" | "onFocusOutside") => {
      const event = new CustomEvent("dismissableLayer.outside", { cancelable: true, detail: { originalEvent } })
      Object.defineProperty(event, "target", { value: originalEvent.target })
      ;(latest.current[handler] as ((event: OutsideEvent<any>) => void) | undefined)?.(event)
      latest.current.onInteractOutside?.(event)
      if (!event.defaultPrevented) latest.current.onDismiss?.()
    }

    // A layer above this one (a select over a dialog) makes everything below inert, so a tap beside it lands on the page root and
    // would count as outside for every layer: only the top layer decides
    const isTop = () => layers[layers.length - 1] === node
    let pendingClick = () => {}
    const onPointerDown = (event: PointerEvent) => {
      pendingClick()
      if (!isTop() || isInside(event.target)) return
      // Touch dismisses when the finger lifts without having moved, so a scroll gesture outside keeps the layer open. Not on the click:
      // iOS sends none for a tap on a non-interactive element (the inert page root).
      if (event.pointerType === "touch") {
        const { clientX, clientY, pointerId } = event
        const cancel = () => {
          document.removeEventListener("pointerup", onUp, true)
          document.removeEventListener("pointercancel", cancel, true)
        }
        const onUp = (up: PointerEvent) => {
          cancel()
          if (up.pointerId !== pointerId || Math.hypot(up.clientX - clientX, up.clientY - clientY) > TAP_SLOP) return
          dismiss(event, "onPointerDownOutside")
          // The click that follows would land on whatever the layer covered
          const swallow = (click: Event) => {
            click.stopPropagation()
            click.preventDefault()
          }
          document.addEventListener("click", swallow, { capture: true, once: true })
          setTimeout(() => document.removeEventListener("click", swallow, true), 400)
        }
        pendingClick = cancel
        document.addEventListener("pointerup", onUp, true)
        document.addEventListener("pointercancel", cancel, true)
      } else dismiss(event, "onPointerDownOutside")
    }
    const onFocusIn = (event: FocusEvent) => {
      if (isTop() && !isInside(event.target)) dismiss(event, "onFocusOutside")
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || layers[layers.length - 1] !== node) return
      latest.current.onEscapeKeyDown?.(event)
      if (!event.defaultPrevented && latest.current.onDismiss) {
        event.preventDefault()
        latest.current.onDismiss()
      }
    }

    // Registered a tick late so the pointer down that opened the layer is not treated as outside
    const timer = setTimeout(() => document.addEventListener("pointerdown", onPointerDown))
    document.addEventListener("focusin", onFocusIn)
    document.addEventListener("keydown", onKeyDown, true)
    return () => {
      clearTimeout(timer)
      layers.splice(layers.indexOf(node), 1)
      document.removeEventListener("pointerdown", onPointerDown)
      pendingClick()
      document.removeEventListener("focusin", onFocusIn)
      document.removeEventListener("keydown", onKeyDown, true)
    }
  }, [node])
}
