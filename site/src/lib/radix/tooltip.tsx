import { createContext, type ComponentChildren, type RefObject } from "preact"
import { useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from "preact/hooks"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { useDismissableLayer, type DismissableLayerProps } from "./internal/dismissable-layer"
import { forwardRef } from "./internal/forward-ref"
import * as PopperPrimitive from "./internal/popper"
import * as PortalPrimitive from "./internal/portal"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps } from "./internal/primitive"
import { mergeStyles } from "./internal/style"
import { useControllableState } from "./internal/use-controllable-state"
import { VisuallyHidden } from "./internal/visually-hidden"

const TOOLTIP_OPEN = "tooltip.open"
// How long a tooltip waits after the pointer left the trigger or the content, so it can cross the gap between them
const CLOSE_GRACE = 100

const [TooltipProviderContext, useTooltipProviderContext] = createStrictContext<{
  isOpenDelayedRef: RefObject<boolean>
  delayDuration: number
  onOpen(): void
  onClose(): void
}>("TooltipProvider")

const [TooltipContext, useTooltipContext] = createStrictContext<{
  contentId: string
  open: boolean
  stateAttribute: "closed" | "delayed-open" | "instant-open"
  onTriggerEnter(): void
  onTriggerLeave(): void
  onContentEnter(): void
  onContentLeave(): void
  onOpen(): void
  onClose(): void
}>("Tooltip")

// The arrow also sits in the content copied for screen readers; only the visible one is rendered
const HiddenContentContext = createContext(false)

// shortcut: no pointer grace polygon, `disableHoverableContent` or close-on-scroll; the tooltip stays hoverable through the
// CLOSE_GRACE delay instead, add the polygon if tooltips flicker when the pointer crosses a wide gap
function TooltipProvider({
  delayDuration = 700,
  skipDelayDuration = 300,
  children,
}: {
  delayDuration?: number
  skipDelayDuration?: number
  children?: ComponentChildren
}) {
  const isOpenDelayedRef = useRef(true)
  const skipDelayTimerRef = useRef(0)
  useEffect(() => () => clearTimeout(skipDelayTimerRef.current), [])
  return (
    <TooltipProviderContext
      value={{
        isOpenDelayedRef,
        delayDuration,
        onOpen: useCallback(() => {
          clearTimeout(skipDelayTimerRef.current)
          isOpenDelayedRef.current = false
        }, []),
        onClose: useCallback(() => {
          clearTimeout(skipDelayTimerRef.current)
          skipDelayTimerRef.current = window.setTimeout(() => (isOpenDelayedRef.current = true), skipDelayDuration)
        }, [skipDelayDuration]),
      }}
    >
      {children}
    </TooltipProviderContext>
  )
}

function Tooltip({
  open: openProp,
  defaultOpen,
  onOpenChange,
  delayDuration: delayDurationProp,
  children,
}: {
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?(open: boolean): void
  delayDuration?: number
  children?: ComponentChildren
}) {
  const providerContext = useTooltipProviderContext("Tooltip")
  const contentId = useId()
  const openTimerRef = useRef(0)
  const closeTimerRef = useRef(0)
  const wasOpenDelayedRef = useRef(false)
  const delayDuration = delayDurationProp ?? providerContext.delayDuration
  const [open, setOpen] = useControllableState({
    prop: openProp,
    defaultProp: defaultOpen ?? false,
    onChange(open) {
      if (open) {
        providerContext.onOpen()
        // Only one tooltip at a time
        document.dispatchEvent(new CustomEvent(TOOLTIP_OPEN))
      } else providerContext.onClose()
      onOpenChange?.(open)
    },
  })
  const stateAttribute = useMemo(() => (open ? (wasOpenDelayedRef.current ? "delayed-open" : "instant-open") : "closed"), [open])

  const handleOpen = useCallback(() => {
    clearTimeout(openTimerRef.current)
    clearTimeout(closeTimerRef.current)
    wasOpenDelayedRef.current = false
    setOpen(true)
  }, [setOpen])
  const handleClose = useCallback(() => {
    clearTimeout(openTimerRef.current)
    clearTimeout(closeTimerRef.current)
    setOpen(false)
  }, [setOpen])
  const handleDelayedOpen = useCallback(() => {
    clearTimeout(openTimerRef.current)
    clearTimeout(closeTimerRef.current)
    openTimerRef.current = window.setTimeout(() => {
      wasOpenDelayedRef.current = true
      setOpen(true)
    }, delayDuration)
  }, [delayDuration, setOpen])
  // Also drops a pending delayed open
  const scheduleClose = useCallback(() => {
    clearTimeout(openTimerRef.current)
    clearTimeout(closeTimerRef.current)
    closeTimerRef.current = window.setTimeout(handleClose, CLOSE_GRACE)
  }, [handleClose])
  useEffect(
    () => () => {
      clearTimeout(openTimerRef.current)
      clearTimeout(closeTimerRef.current)
    },
    []
  )

  return (
    <PopperPrimitive.Root>
      <TooltipContext
        value={{
          contentId,
          open,
          stateAttribute,
          onTriggerEnter: () => (providerContext.isOpenDelayedRef.current ? handleDelayedOpen() : handleOpen()),
          onTriggerLeave: scheduleClose,
          onContentEnter: () => clearTimeout(closeTimerRef.current),
          onContentLeave: scheduleClose,
          onOpen: handleOpen,
          onClose: handleClose,
        }}
      >
        {children}
      </TooltipContext>
    </PopperPrimitive.Root>
  )
}

const TooltipTrigger = forwardRef<PrimitiveProps<"button">>((props, ref) => {
  const context = useTooltipContext("TooltipTrigger")
  const isPointerDownRef = useRef(false)
  const hasPointerMoveOpenedRef = useRef(false)
  const handlePointerUp = useCallback(() => (isPointerDownRef.current = false), [])
  useEffect(() => () => document.removeEventListener("pointerup", handlePointerUp), [handlePointerUp])

  return (
    <PopperPrimitive.Anchor asChild>
      {/* No type="button": tooltip triggers are often anchors, where `type` is a MIME type */}
      <Primitive.button
        aria-describedby={context.open ? context.contentId : undefined}
        data-state={context.stateAttribute}
        {...props}
        ref={ref}
        onPointerMove={composeEventHandlers(props.onPointerMove, (event) => {
          if (event.pointerType === "touch") return
          if (!hasPointerMoveOpenedRef.current) {
            context.onTriggerEnter()
            hasPointerMoveOpenedRef.current = true
          }
        })}
        onPointerLeave={composeEventHandlers(props.onPointerLeave, () => {
          context.onTriggerLeave()
          hasPointerMoveOpenedRef.current = false
        })}
        onPointerDown={composeEventHandlers(props.onPointerDown, () => {
          if (context.open) context.onClose()
          isPointerDownRef.current = true
          document.addEventListener("pointerup", handlePointerUp, { once: true })
        })}
        // The focus that follows a press must not reopen what the press just closed
        onFocus={composeEventHandlers(props.onFocus, () => {
          if (!isPointerDownRef.current) context.onOpen()
        })}
        onBlur={composeEventHandlers(props.onBlur, context.onClose)}
        onClick={composeEventHandlers(props.onClick, context.onClose)}
      />
    </PopperPrimitive.Anchor>
  )
})

function TooltipPortal({ children }: { children?: ComponentChildren }) {
  return <PortalPrimitive.Root>{children}</PortalPrimitive.Root>
}

type TooltipContentProps = PopperPrimitive.PopperContentProps & Pick<DismissableLayerProps, "onEscapeKeyDown" | "onPointerDownOutside">

const TooltipContentImpl = forwardRef<TooltipContentProps>(
  ({ children, "aria-label": ariaLabel, onEscapeKeyDown, onPointerDownOutside, onPointerEnter, onPointerLeave, side = "top", style, ...contentProps }, forwardedRef) => {
    const context = useTooltipContext("TooltipContent")
    const [node, setNode] = useState<HTMLElement | null>(null)
    const ref = useComposedRefs(forwardedRef, setNode)
    const { onClose } = context

    useEffect(() => {
      document.addEventListener(TOOLTIP_OPEN, onClose)
      return () => document.removeEventListener(TOOLTIP_OPEN, onClose)
    }, [onClose])
    useDismissableLayer(node, { onEscapeKeyDown, onPointerDownOutside, onFocusOutside: (event) => event.preventDefault(), onDismiss: onClose })

    return (
      <PopperPrimitive.Content
        data-state={context.stateAttribute}
        side={side}
        {...contentProps}
        ref={ref}
        onPointerEnter={composeEventHandlers(onPointerEnter, context.onContentEnter)}
        onPointerLeave={composeEventHandlers(onPointerLeave, context.onContentLeave)}
        style={mergeStyles(style, {
          "--radix-tooltip-content-transform-origin": "var(--radix-popper-transform-origin)",
          "--radix-tooltip-content-available-width": "var(--radix-popper-available-width)",
          "--radix-tooltip-content-available-height": "var(--radix-popper-available-height)",
          "--radix-tooltip-trigger-width": "var(--radix-popper-anchor-width)",
          "--radix-tooltip-trigger-height": "var(--radix-popper-anchor-height)",
        })}
      >
        {children}
        <HiddenContentContext.Provider value>
          <VisuallyHidden id={context.contentId} role="tooltip">
            {ariaLabel || children}
          </VisuallyHidden>
        </HiddenContentContext.Provider>
      </PopperPrimitive.Content>
    )
  }
)

const TooltipContent = forwardRef<TooltipContentProps>((props, ref) => {
  const context = useTooltipContext("TooltipContent")
  return (
    <Presence present={context.open}>
      <TooltipContentImpl {...props} ref={ref} />
    </Presence>
  )
})

const TooltipArrow = forwardRef<PrimitiveProps<"svg">>((props, ref) => {
  const isInsideHiddenContent = useContext(HiddenContentContext)
  return isInsideHiddenContent ? null : <PopperPrimitive.Arrow {...props} ref={ref} />
})

export {
  TooltipProvider as Provider,
  Tooltip as Root,
  TooltipTrigger as Trigger,
  TooltipPortal as Portal,
  TooltipContent as Content,
  TooltipArrow as Arrow,
}
export type { TooltipContentProps }
