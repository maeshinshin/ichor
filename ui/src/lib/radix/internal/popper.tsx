import { arrow as arrowMiddleware, autoUpdate, computePosition, flip, offset, shift, size, type Middleware, type MiddlewareData, type Placement } from "@floating-ui/dom"
import type { ComponentChildren, RefObject } from "preact"
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks"
import { useComposedRefs } from "./compose-refs"
import { createStrictContext } from "./create-context"
import { forwardRef } from "./forward-ref"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./primitive"
import { mergeStyles } from "./style"
import { useSize } from "./use-size"

type Side = "top" | "right" | "bottom" | "left"
type Align = "start" | "center" | "end"

const OPPOSITE_SIDE: Record<Side, Side> = { top: "bottom", right: "left", bottom: "top", left: "right" }

// What the content is placed against: an element, or a virtual point such as the pointer of a context menu
type Measurable = { getBoundingClientRect(): DOMRect }

const [PopperProvider, usePopperContext] = createStrictContext<{
  anchor: Measurable | null
  onAnchorChange(anchor: Measurable | null): void
  placement?: Placement
  onPlacementChange(placement: Placement | undefined): void
}>("Popper")

const [PopperContentProvider, usePopperContentContext] = createStrictContext<{
  placedSide: Side
  onArrowChange(arrow: HTMLElement | null): void
  arrowX?: number
  arrowY?: number
  shouldHideArrow: boolean
}>("PopperContent")

const splitPlacement = (placement: Placement) => placement.split("-") as [Side, Align?]

// Whole device pixels keep the content crisp; only called once the content is positioned, which never happens on the server
const roundToDevicePixel = (value: number) => {
  const pixelRatio = window.devicePixelRatio || 1
  return Math.round(value * pixelRatio) / pixelRatio
}

// Where the open animation should grow from: the arrow tip when it is centered, otherwise the aligned edge
const transformOrigin = ({ arrowWidth, arrowHeight }: { arrowWidth: number; arrowHeight: number }): Middleware => ({
  name: "transformOrigin",
  fn({ placement, rects, middlewareData }) {
    const isArrowHidden = middlewareData.arrow?.centerOffset !== 0
    const width = isArrowHidden ? 0 : arrowWidth
    const height = isArrowHidden ? 0 : arrowHeight
    const [side, align = "center"] = splitPlacement(placement)
    const noArrowAlign = { start: "0%", center: "50%", end: "100%" }[align]
    const xCenter = `${(middlewareData.arrow?.x ?? 0) + width / 2}px`
    const yCenter = `${(middlewareData.arrow?.y ?? 0) + height / 2}px`
    const [x, y] = {
      bottom: [isArrowHidden ? noArrowAlign : xCenter, `${-height}px`],
      top: [isArrowHidden ? noArrowAlign : xCenter, `${rects.floating.height + height}px`],
      right: [`${-height}px`, isArrowHidden ? noArrowAlign : yCenter],
      left: [`${rects.floating.width + height}px`, isArrowHidden ? noArrowAlign : yCenter],
    }[side]
    return { data: { x, y } }
  },
})

function Popper({ children }: { children?: ComponentChildren }) {
  const [anchor, onAnchorChange] = useState<Measurable | null>(null)
  const [placement, onPlacementChange] = useState<Placement>()
  return <PopperProvider value={{ anchor, onAnchorChange, placement, onPlacementChange }}>{children}</PopperProvider>
}

const PopperAnchor = forwardRef<PrimitiveProps<"div"> & { virtualRef?: RefObject<Measurable> }>(({ virtualRef, ...props }, forwardedRef) => {
  const context = usePopperContext("PopperAnchor")
  const ref = useRef<HTMLDivElement>(null)
  const composedRefs = useComposedRefs(forwardedRef, ref)
  const anchorRef = useRef<Measurable | null>(null)

  // Re-asserted after every render so a swapped anchor element is picked up, and adopted when the registered one left the page
  // (a trigger that stood in until a custom anchor took over)
  useEffect(() => {
    const previous = anchorRef.current
    anchorRef.current = virtualRef?.current ?? ref.current
    // A virtual anchor has no `isConnected`
    if (previous !== anchorRef.current || (context.anchor && (context.anchor as Partial<Element>).isConnected === false)) context.onAnchorChange(anchorRef.current)
  })

  // The anchor reports where the content ended up, for styling that depends on it
  const [side, align = "center"] = context.placement ? splitPlacement(context.placement) : []
  return virtualRef ? null : <Primitive.div data-radix-popper-side={side} data-radix-popper-align={side && align} {...props} ref={composedRefs} />
})

type PopperContentProps = PrimitiveWith<
  "div",
  { side?: Side; sideOffset?: number; align?: Align; alignOffset?: number; collisionPadding?: number; onPlaced?(): void }
>

// shortcut: always flips and shifts to stay in the viewport (no `avoidCollisions`, `collisionBoundary`, `sticky`, `hideWhenDetached`,
// `arrowPadding`, per-side `collisionPadding`); add them when a component needs to tune collisions
const PopperContent = forwardRef<PopperContentProps>(
  ({ side = "bottom", sideOffset = 0, align = "center", alignOffset = 0, collisionPadding = 0, onPlaced, style, ...contentProps }, forwardedRef) => {
    const context = usePopperContext("PopperContent")
    const [content, setContent] = useState<HTMLElement | null>(null)
    const [wrapper, setWrapper] = useState<HTMLElement | null>(null)
    const [arrow, setArrow] = useState<HTMLElement | null>(null)
    const [zIndex, setZIndex] = useState<string>()
    const [position, setPosition] = useState<{ x: number; y: number; placement: Placement; middlewareData: MiddlewareData }>()
    const composedRefs = useComposedRefs(forwardedRef, setContent)
    const arrowSize = useSize(arrow)
    const arrowWidth = arrowSize?.width ?? 0
    const arrowHeight = arrowSize?.height ?? 0
    const desiredPlacement = (align === "center" ? side : `${side}-${align}`) as Placement

    useLayoutEffect(() => {
      const anchor = context.anchor
      if (!anchor || !wrapper) return
      // The content lives in a portal, so only the viewport can clip it
      const overflow = { boundary: [] as Element[], padding: collisionPadding }
      let active = true
      const update = () =>
        computePosition(anchor, wrapper, {
          strategy: "fixed",
          placement: desiredPlacement,
          middleware: [
            offset({ mainAxis: sideOffset + arrowHeight, alignmentAxis: alignOffset }),
            shift({ ...overflow, mainAxis: true, crossAxis: false }),
            flip(overflow),
            size({
              ...overflow,
              apply({ elements, rects, availableWidth, availableHeight }) {
                const { style } = elements.floating
                style.setProperty("--radix-popper-available-width", `${availableWidth}px`)
                style.setProperty("--radix-popper-available-height", `${availableHeight}px`)
                style.setProperty("--radix-popper-anchor-width", `${rects.reference.width}px`)
                style.setProperty("--radix-popper-anchor-height", `${rects.reference.height}px`)
              },
            }),
            ...(arrow ? [arrowMiddleware({ element: arrow })] : []),
            transformOrigin({ arrowWidth, arrowHeight }),
          ],
        }).then((result) => active && setPosition(result))
      const stop = autoUpdate(anchor, wrapper, update)
      return () => {
        active = false
        stop()
      }
    }, [context.anchor, wrapper, arrow, desiredPlacement, sideOffset, alignOffset, collisionPadding, arrowWidth, arrowHeight])

    // The wrapper creates the stacking context, so it has to carry the z-index the content asks for
    useLayoutEffect(() => {
      if (content) setZIndex(getComputedStyle(content).zIndex)
    }, [content])

    const isPositioned = !!position
    useEffect(() => {
      if (isPositioned) onPlaced?.()
    }, [isPositioned])

    const placement = position?.placement ?? desiredPlacement
    const [placedSide, placedAlign = "center"] = splitPlacement(placement)
    const { onPlacementChange } = context
    useLayoutEffect(() => {
      onPlacementChange(placement)
      return () => onPlacementChange(undefined)
    }, [placement, onPlacementChange])
    const arrowData = position?.middlewareData.arrow
    const origin = position?.middlewareData.transformOrigin as { x: string; y: string } | undefined

    return (
      <div
        ref={setWrapper}
        data-radix-popper-content-wrapper=""
        dir={contentProps.dir}
        style={{
          position: "fixed",
          left: 0,
          top: 0,
          // Parked off the page until the first measurement
          transform: position ? `translate(${roundToDevicePixel(position.x)}px, ${roundToDevicePixel(position.y)}px)` : "translate(0, -200%)",
          minWidth: "max-content",
          zIndex,
          "--radix-popper-transform-origin": [origin?.x, origin?.y].join(" "),
        }}
      >
        <PopperContentProvider
          value={{ placedSide, onArrowChange: setArrow, arrowX: arrowData?.x, arrowY: arrowData?.y, shouldHideArrow: arrowData?.centerOffset !== 0 }}
        >
          <Primitive.div
            data-side={placedSide}
            data-align={placedAlign}
            {...contentProps}
            ref={composedRefs}
            // Animations must not start before the final side is known
            style={mergeStyles(style, { animation: position ? undefined : "none" })}
          />
        </PopperContentProvider>
      </div>
    )
  }
)

const PopperArrow = forwardRef<PrimitiveProps<"svg">>(({ asChild, children, style, ...arrowProps }, ref) => {
  const content = usePopperContentContext("PopperArrow")
  const baseSide = OPPOSITE_SIDE[content.placedSide]
  return (
    // The span is what gets measured: ResizeObserver reports the bounding box of an svg's paths
    <span
      ref={content.onArrowChange}
      style={{
        position: "absolute",
        // Preact 11 does not append px to numbers
        left: content.arrowX === undefined ? undefined : `${content.arrowX}px`,
        top: content.arrowY === undefined ? undefined : `${content.arrowY}px`,
        [baseSide]: 0,
        transformOrigin: { top: "", right: "0 0", bottom: "center 0", left: "100% 0" }[content.placedSide],
        transform: {
          top: "translateY(100%)",
          right: "translateY(50%) rotate(90deg) translateX(-50%)",
          bottom: "rotate(180deg)",
          left: "translateY(50%) rotate(-90deg) translateX(50%)",
        }[content.placedSide],
        visibility: content.shouldHideArrow ? "hidden" : undefined,
      }}
    >
      <Primitive.svg width={10} height={5} viewBox="0 0 30 10" preserveAspectRatio="none" {...arrowProps} ref={ref} asChild={asChild} style={mergeStyles(style, { display: "block" })}>
        {asChild ? children : <polygon points="0,0 30,0 15,10" />}
      </Primitive.svg>
    </span>
  )
})

export { Popper as Root, PopperAnchor as Anchor, PopperContent as Content, PopperArrow as Arrow }
export type { PopperContentProps }
