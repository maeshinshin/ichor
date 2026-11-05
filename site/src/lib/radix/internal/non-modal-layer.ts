import type { RefObject } from "preact"
import { useRef } from "preact/hooks"
import type { OutsideEvent } from "./dismissable-layer"

// What a layer that leaves the page usable (popover, non-modal dialog) does around outside interactions: the trigger already
// toggles it, and after an outside interaction focus belongs to wherever the user went
export function useNonModalLayer(
  triggerRef: RefObject<HTMLElement | null>,
  { onCloseAutoFocus, onInteractOutside }: { onCloseAutoFocus?(event: Event): void; onInteractOutside?(event: OutsideEvent<PointerEvent | FocusEvent>): void }
) {
  const hasInteractedOutside = useRef(false)
  const hasPointerDownOutside = useRef(false)

  return {
    onUnmountAutoFocus(event: Event) {
      onCloseAutoFocus?.(event)
      if (!event.defaultPrevented) {
        // Without a trigger the focus scope returns focus to what had it before opening
        if (hasInteractedOutside.current) event.preventDefault()
        else if (triggerRef.current) {
          triggerRef.current.focus()
          event.preventDefault()
        }
      }
      hasInteractedOutside.current = false
      hasPointerDownOutside.current = false
    },
    onInteractOutside(event: OutsideEvent<PointerEvent | FocusEvent>) {
      onInteractOutside?.(event)
      if (!event.defaultPrevented) {
        hasInteractedOutside.current = true
        if (event.detail.originalEvent.type === "pointerdown") hasPointerDownOutside.current = true
      }
      // Dismissing on the trigger would close the layer and let the trigger's own click reopen it
      if (triggerRef.current?.contains(event.target as Node)) event.preventDefault()
      // Safari reports a focus outside right after a pointer down outside when the trigger sits in a tabindex container
      if (event.detail.originalEvent.type === "focusin" && hasPointerDownOutside.current) event.preventDefault()
    },
  }
}
