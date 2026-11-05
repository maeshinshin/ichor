import { cloneElement, toChildArray, type ComponentChildren, type HTMLAttributes } from "preact"
import { useComposedRefs } from "./internal/compose-refs"
import { getElementRef, isElement } from "./internal/element"
import { forwardRef } from "./internal/forward-ref"
import { mergeProps } from "./internal/merge-props"

type SlotProps = HTMLAttributes<any> & { children?: ComponentChildren }

const Slot = forwardRef<SlotProps>(({ children, ...slotProps }, forwardedRef) => {
  const elements = toChildArray(children)
  const child = elements[0]
  const ref = useComposedRefs(forwardedRef, isElement(child) ? getElementRef(child) : undefined)

  if (elements.length > 1) throw new Error("Slot expects a single child element")
  if (!isElement(child)) return null
  return cloneElement(child, { ...mergeProps(slotProps, child.props), ref })
})

export { Slot as Root }
