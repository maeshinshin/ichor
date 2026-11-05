import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "preact/hooks"
import { useDirection, type Direction } from "./direction"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { mergeStyles } from "./internal/style"

type Orientation = "horizontal" | "vertical"
type Axis = "x" | "y"
type Sizes = { viewport: number; content: number; scrollbar: { size: number; paddingStart: number; paddingEnd: number } }

const MIN_THUMB_SIZE = 18
// shortcut: always `type="hover"` with a fixed 600ms hide delay, ltr scroll math, and wheeling over the scrollbar does not scroll; add if needed
const HIDE_DELAY = 600

const [ScrollAreaProvider, useScrollAreaContext] = createStrictContext<{
  dir: Direction
  scrollArea: HTMLElement | null
  viewport: HTMLElement | null
  onViewportChange(node: HTMLElement | null): void
  content: HTMLElement | null
  onContentChange(node: HTMLElement | null): void
  enabled: Record<Axis, boolean>
  onEnabledChange(axis: Axis, enabled: boolean): void
  bars: Record<Axis, HTMLElement | null>
  onBarChange(axis: Axis, node: HTMLElement | null): void
  corner: { width: number; height: number }
  onCornerChange(width: number, height: number): void
}>("ScrollArea")
const [ScrollbarProvider, useScrollbarContext] = createStrictContext<{
  hasThumb: boolean
  onThumbChange(node: HTMLElement | null): void
  onThumbPointerDown(event: PointerEvent): void
}>("ScrollAreaScrollbar")

const toInt = (value?: string) => (value ? parseInt(value, 10) : 0)
const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value))
const scale = (input: [number, number], output: [number, number]) => (value: number) =>
  input[0] === input[1] || output[0] === output[1] ? output[0] : output[0] + ((output[1] - output[0]) / (input[1] - input[0])) * (value - input[0])

const getThumbSize = (sizes: Sizes) =>
  Math.max((sizes.scrollbar.size - sizes.scrollbar.paddingStart - sizes.scrollbar.paddingEnd) * (sizes.viewport / sizes.content), MIN_THUMB_SIZE)

function getThumbOffset(scrollPos: number, sizes: Sizes) {
  const maxScrollPos = sizes.content - sizes.viewport
  const maxThumbPos = sizes.scrollbar.size - sizes.scrollbar.paddingStart - sizes.scrollbar.paddingEnd - getThumbSize(sizes)
  return scale([0, maxScrollPos], [0, maxThumbPos])(clamp(scrollPos, 0, maxScrollPos))
}

function getScrollPosition(pointerPos: number, pointerOffset: number, sizes: Sizes) {
  const thumbSize = getThumbSize(sizes)
  const offset = pointerOffset || thumbSize / 2
  const minPointerPos = sizes.scrollbar.paddingStart + offset
  const maxPointerPos = sizes.scrollbar.size - sizes.scrollbar.paddingEnd - (thumbSize - offset)
  return scale([minPointerPos, maxPointerPos], [0, sizes.content - sizes.viewport])(pointerPos)
}

const ScrollArea = forwardRef<PrimitiveProps<"div">>(({ dir, ...props }, forwardedRef) => {
  const direction = useDirection(dir as Direction | undefined)
  const [scrollArea, setScrollArea] = useState<HTMLElement | null>(null)
  const [viewport, setViewport] = useState<HTMLElement | null>(null)
  const [content, setContent] = useState<HTMLElement | null>(null)
  const [enabled, setEnabled] = useState({ x: false, y: false })
  const [bars, setBars] = useState<Record<Axis, HTMLElement | null>>({ x: null, y: null })
  const [corner, setCorner] = useState({ width: 0, height: 0 })
  const composedRefs = useComposedRefs(forwardedRef, setScrollArea)

  return (
    <ScrollAreaProvider
      value={{
        dir: direction,
        scrollArea,
        viewport,
        onViewportChange: setViewport,
        content,
        onContentChange: setContent,
        enabled,
        onEnabledChange: useCallback((axis, value) => setEnabled((prev) => ({ ...prev, [axis]: value })), []),
        bars,
        onBarChange: useCallback((axis, node) => setBars((prev) => ({ ...prev, [axis]: node })), []),
        corner,
        onCornerChange: useCallback((width, height) => setCorner({ width, height }), []),
      }}
    >
      <Primitive.div
        dir={direction}
        {...props}
        ref={composedRefs}
        style={mergeStyles(
          {
            position: "relative",
            "--radix-scroll-area-corner-width": `${corner.width}px`,
            "--radix-scroll-area-corner-height": `${corner.height}px`,
          },
          props.style
        )}
      />
    </ScrollAreaProvider>
  )
})

const VIEWPORT_STYLE =
  "[data-radix-scroll-area-viewport]{scrollbar-width:none;-ms-overflow-style:none;-webkit-overflow-scrolling:touch;}[data-radix-scroll-area-viewport]::-webkit-scrollbar{display:none}"

const ScrollAreaViewport = forwardRef<PrimitiveProps<"div">>(({ children, ...viewportProps }, forwardedRef) => {
  const context = useScrollAreaContext("ScrollAreaViewport")
  const composedRefs = useComposedRefs(forwardedRef, context.onViewportChange)
  return (
    <>
      <style dangerouslySetInnerHTML={{ __html: VIEWPORT_STYLE }} />
      <Primitive.div
        data-radix-scroll-area-viewport=""
        {...viewportProps}
        ref={composedRefs}
        style={mergeStyles(
          { overflowX: context.enabled.x ? "scroll" : "hidden", overflowY: context.enabled.y ? "scroll" : "hidden" },
          viewportProps.style
        )}
      >
        {/* display: table keeps the content as wide as it needs to be so the viewport can overflow horizontally */}
        <div ref={context.onContentChange} style={{ minWidth: "100%", display: "table" }}>
          {children}
        </div>
      </Primitive.div>
    </>
  )
})

const ScrollAreaScrollbar = forwardRef<PrimitiveWith<"div", { orientation?: Orientation }>>(
  ({ orientation = "vertical", ...props }, ref) => {
    const { scrollArea, viewport, content, onEnabledChange } = useScrollAreaContext("ScrollAreaScrollbar")
    const axis = orientation === "horizontal" ? "x" : "y"
    const [hovering, setHovering] = useState(false)
    const [hasOverflow, setHasOverflow] = useState(false)

    // The viewport has to scroll on this axis for as long as a scrollbar for it exists
    useEffect(() => {
      onEnabledChange(axis, true)
      return () => onEnabledChange(axis, false)
    }, [axis, onEnabledChange])

    useEffect(() => {
      if (!scrollArea) return
      let hideTimer = 0
      const show = () => {
        window.clearTimeout(hideTimer)
        setHovering(true)
      }
      const hide = () => {
        hideTimer = window.setTimeout(() => setHovering(false), HIDE_DELAY)
      }
      scrollArea.addEventListener("pointerenter", show)
      scrollArea.addEventListener("pointerleave", hide)
      return () => {
        window.clearTimeout(hideTimer)
        scrollArea.removeEventListener("pointerenter", show)
        scrollArea.removeEventListener("pointerleave", hide)
      }
    }, [scrollArea])

    useLayoutEffect(() => {
      if (!viewport) return
      const measure = () =>
        setHasOverflow(axis === "x" ? viewport.offsetWidth < viewport.scrollWidth : viewport.offsetHeight < viewport.scrollHeight)
      measure()
      const observer = new ResizeObserver(measure)
      observer.observe(viewport)
      if (content) observer.observe(content)
      return () => observer.disconnect()
    }, [viewport, content, axis])

    const visible = hovering && hasOverflow
    return (
      <Presence present={visible}>
        <ScrollAreaScrollbarImpl data-state={visible ? "visible" : "hidden"} {...props} orientation={orientation} ref={ref} />
      </Presence>
    )
  }
)

const ScrollAreaScrollbarImpl = forwardRef<PrimitiveWith<"div", { orientation: Orientation }>>(({ orientation, ...props }, forwardedRef) => {
  const context = useScrollAreaContext("ScrollAreaScrollbar")
  const { viewport, content, onBarChange } = context
  const isHorizontal = orientation === "horizontal"
  const axis = isHorizontal ? "x" : "y"
  const [scrollbar, setScrollbar] = useState<HTMLElement | null>(null)
  const [thumb, setThumb] = useState<HTMLElement | null>(null)
  const [sizes, setSizes] = useState<Sizes>({ viewport: 0, content: 0, scrollbar: { size: 0, paddingStart: 0, paddingEnd: 0 } })
  const pointerOffset = useRef(0)
  const rect = useRef<DOMRect | null>(null)
  const onNode = useCallback(
    (node: HTMLElement | null) => {
      setScrollbar(node)
      onBarChange(axis, node)
    },
    [axis, onBarChange]
  )
  const composedRefs = useComposedRefs(forwardedRef, onNode)

  useLayoutEffect(() => {
    if (!viewport || !scrollbar) return
    const measure = () => {
      const style = getComputedStyle(scrollbar)
      setSizes({
        viewport: isHorizontal ? viewport.offsetWidth : viewport.offsetHeight,
        content: isHorizontal ? viewport.scrollWidth : viewport.scrollHeight,
        scrollbar: {
          size: isHorizontal ? scrollbar.clientWidth : scrollbar.clientHeight,
          paddingStart: toInt(isHorizontal ? style.paddingLeft : style.paddingTop),
          paddingEnd: toInt(isHorizontal ? style.paddingRight : style.paddingBottom),
        },
      })
    }
    measure()
    const observer = new ResizeObserver(measure)
    for (const node of [scrollbar, viewport, content]) if (node) observer.observe(node)
    return () => observer.disconnect()
  }, [viewport, content, scrollbar, isHorizontal])

  useLayoutEffect(() => {
    if (!viewport || !thumb) return
    const update = () => {
      const offset = getThumbOffset(isHorizontal ? viewport.scrollLeft : viewport.scrollTop, sizes)
      thumb.style.transform = isHorizontal ? `translate3d(${offset}px, 0, 0)` : `translate3d(0, ${offset}px, 0)`
    }
    update()
    viewport.addEventListener("scroll", update)
    return () => viewport.removeEventListener("scroll", update)
  }, [viewport, thumb, sizes, isHorizontal])

  function dragScroll(event: PointerEvent) {
    if (!viewport || !rect.current) return
    const pointerPos = isHorizontal ? event.clientX - rect.current.left : event.clientY - rect.current.top
    const next = getScrollPosition(pointerPos, pointerOffset.current, sizes)
    if (isHorizontal) viewport.scrollLeft = next
    else viewport.scrollTop = next
  }

  const ratio = sizes.viewport / sizes.content
  const thumbSize = getThumbSize(sizes)
  const orientationStyle = isHorizontal
    ? {
        bottom: 0,
        left: context.dir === "rtl" ? "var(--radix-scroll-area-corner-width)" : 0,
        right: context.dir === "ltr" ? "var(--radix-scroll-area-corner-width)" : 0,
        "--radix-scroll-area-thumb-width": `${thumbSize}px`,
      }
    : {
        top: 0,
        right: context.dir === "ltr" ? 0 : undefined,
        left: context.dir === "rtl" ? 0 : undefined,
        bottom: "var(--radix-scroll-area-corner-height)",
        "--radix-scroll-area-thumb-height": `${thumbSize}px`,
      }

  return (
    <ScrollbarProvider
      value={{
        hasThumb: ratio > 0 && ratio < 1,
        onThumbChange: setThumb,
        onThumbPointerDown: (event) => {
          const thumbRect = (event.currentTarget as HTMLElement).getBoundingClientRect()
          pointerOffset.current = isHorizontal ? event.clientX - thumbRect.left : event.clientY - thumbRect.top
        },
      }}
    >
      <Primitive.div
        data-orientation={orientation}
        {...props}
        ref={composedRefs}
        style={mergeStyles({ position: "absolute" }, orientationStyle, props.style)}
        onPointerDown={composeEventHandlers(props.onPointerDown, (event) => {
          if (event.button !== 0) return
          ;(event.target as HTMLElement).setPointerCapture(event.pointerId)
          rect.current = scrollbar!.getBoundingClientRect()
          dragScroll(event)
        })}
        onPointerMove={composeEventHandlers(props.onPointerMove, (event) => {
          if ((event.target as HTMLElement).hasPointerCapture(event.pointerId)) dragScroll(event)
        })}
        onPointerUp={composeEventHandlers(props.onPointerUp, (event) => {
          ;(event.target as HTMLElement).releasePointerCapture(event.pointerId)
          rect.current = null
          pointerOffset.current = 0
        })}
      />
    </ScrollbarProvider>
  )
})

const ScrollAreaThumb = forwardRef<PrimitiveProps<"div">>((props, forwardedRef) => {
  const { hasThumb, onThumbChange, onThumbPointerDown } = useScrollbarContext("ScrollAreaThumb")
  const composedRefs = useComposedRefs(forwardedRef, onThumbChange)
  return (
    <Presence present={hasThumb}>
      <Primitive.div
        data-state={hasThumb ? "visible" : "hidden"}
        {...props}
        ref={composedRefs}
        style={mergeStyles(
          { width: "var(--radix-scroll-area-thumb-width)", height: "var(--radix-scroll-area-thumb-height)" },
          props.style
        )}
        onPointerDown={composeEventHandlers(props.onPointerDown, onThumbPointerDown)}
      />
    </Presence>
  )
})

const ScrollAreaCorner = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const { dir, bars, corner, onCornerChange } = useScrollAreaContext("ScrollAreaCorner")

  useLayoutEffect(() => {
    const { x, y } = bars
    if (!x || !y) return
    const measure = () => onCornerChange(y.offsetWidth, x.offsetHeight)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(x)
    observer.observe(y)
    return () => {
      observer.disconnect()
      onCornerChange(0, 0)
    }
  }, [bars, onCornerChange])

  if (!bars.x || !bars.y || !corner.width || !corner.height) return null
  return (
    <Primitive.div
      {...props}
      ref={ref}
      style={mergeStyles(
        {
          width: `${corner.width}px`,
          height: `${corner.height}px`,
          position: "absolute",
          right: dir === "ltr" ? 0 : undefined,
          left: dir === "rtl" ? 0 : undefined,
          bottom: 0,
        },
        props.style
      )}
    />
  )
})

export { ScrollArea as Root, ScrollAreaViewport as Viewport, ScrollAreaScrollbar, ScrollAreaThumb, ScrollAreaCorner as Corner }
