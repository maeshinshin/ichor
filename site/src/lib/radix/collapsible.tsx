import { useEffect, useId, useLayoutEffect, useRef, useState } from "preact/hooks"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { mergeStyles } from "./internal/style"
import { useControllableState } from "./internal/use-controllable-state"

const [CollapsibleProvider, useCollapsibleContext] = createStrictContext<{
  contentId: string
  disabled?: boolean
  open: boolean
  onOpenToggle(): void
}>("Collapsible")

const getState = (open?: boolean) => (open ? "open" : "closed")

const Collapsible = forwardRef<
  PrimitiveWith<"div", { open?: boolean; defaultOpen?: boolean; disabled?: boolean; onOpenChange?(open: boolean): void }>
>(({ open: openProp, defaultOpen, disabled, onOpenChange, ...collapsibleProps }, ref) => {
  const [open, setOpen] = useControllableState({ prop: openProp, defaultProp: defaultOpen ?? false, onChange: onOpenChange })
  return (
    <CollapsibleProvider value={{ disabled, contentId: useId(), open, onOpenToggle: () => setOpen((prev) => !prev) }}>
      <Primitive.div data-state={getState(open)} data-disabled={disabled ? "" : undefined} {...collapsibleProps} ref={ref} />
    </CollapsibleProvider>
  )
})

const CollapsibleTrigger = forwardRef<PrimitiveProps<"button">>((props, ref) => {
  const context = useCollapsibleContext("CollapsibleTrigger")
  return (
    <Primitive.button
      type="button"
      aria-controls={context.open ? context.contentId : undefined}
      aria-expanded={context.open}
      data-state={getState(context.open)}
      data-disabled={context.disabled ? "" : undefined}
      disabled={context.disabled}
      {...props}
      ref={ref}
      onClick={composeEventHandlers(props.onClick, context.onOpenToggle)}
    />
  )
})

const CollapsibleContentImpl = forwardRef<PrimitiveProps<"div"> & { present: boolean }>(
  ({ present, children, ...contentProps }, forwardedRef) => {
    const context = useCollapsibleContext("CollapsibleContent")
    const [isPresent, setIsPresent] = useState(present)
    const ref = useRef<HTMLDivElement>(null)
    const composedRefs = useComposedRefs(forwardedRef, ref)
    const heightRef = useRef(0)
    const widthRef = useRef(0)
    // When opening, render immediately to measure; when closing, keep rendering until `present` flips after the animation
    const isOpen = context.open || isPresent
    const isMountAnimationPreventedRef = useRef(isOpen)
    const originalStylesRef = useRef<{ transitionDuration: string; animationName: string } | undefined>(undefined)

    useEffect(() => {
      const frame = requestAnimationFrame(() => (isMountAnimationPreventedRef.current = false))
      return () => cancelAnimationFrame(frame)
    }, [])

    useLayoutEffect(() => {
      const node = ref.current
      if (!node) return
      originalStylesRef.current ||= { transitionDuration: node.style.transitionDuration, animationName: node.style.animationName }
      // Block animations and transitions so the element renders at its full size while measuring
      node.style.transitionDuration = "0s"
      node.style.animationName = "none"
      const rect = node.getBoundingClientRect()
      heightRef.current = rect.height
      widthRef.current = rect.width
      if (!isMountAnimationPreventedRef.current) {
        node.style.transitionDuration = originalStylesRef.current.transitionDuration
        node.style.animationName = originalStylesRef.current.animationName
      }
      setIsPresent(present)
    }, [context.open, present])

    return (
      <Primitive.div
        data-state={getState(context.open)}
        data-disabled={context.disabled ? "" : undefined}
        id={context.contentId}
        hidden={!isOpen}
        {...contentProps}
        ref={composedRefs}
        style={mergeStyles(
          {
            "--radix-collapsible-content-height": heightRef.current ? `${heightRef.current}px` : undefined,
            "--radix-collapsible-content-width": widthRef.current ? `${widthRef.current}px` : undefined,
          },
          contentProps.style
        )}
      >
        {isOpen && children}
      </Primitive.div>
    )
  }
)

const CollapsibleContent = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const context = useCollapsibleContext("CollapsibleContent")
  return <Presence present={context.open}>{({ present }) => <CollapsibleContentImpl {...props} present={present} ref={ref} />}</Presence>
})

export { Collapsible as Root, CollapsibleTrigger, CollapsibleContent }
