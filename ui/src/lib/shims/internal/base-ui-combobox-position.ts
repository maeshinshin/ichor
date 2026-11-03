import { autoUpdate, computePosition, flip, limitShift, offset, shift, size, type Middleware, type Placement } from "@floating-ui/dom"
import { useLayoutEffect, useState } from "preact/hooks"

export type Side = "top" | "bottom" | "left" | "right" | "inline-end" | "inline-start"
export type Align = "start" | "center" | "end"
type PhysicalSide = "top" | "bottom" | "left" | "right"

export type Anchor = Element | null | { current: Element | null } | (() => Element | null)
export const resolveAnchor = (anchor: Anchor | undefined) => (typeof anchor === "function" ? anchor() : anchor && "current" in anchor ? anchor.current : anchor) ?? null

type Placed = { x: number; y: number; side: PhysicalSide; align: Align; vars: Record<string, string> }

// Base UI's anchor positioning (`useAnchorPositioning`) on @floating-ui/dom: dropdown collision avoidance, a flipped side stays while the
// list shrinks (`lazyFlip`), and the same CSS variables on the positioner.
// shortcut: no arrow, `hide` (data-anchor-hidden), `sticky`, `collisionBoundary`, per-side `collisionPadding`, function offsets or virtual
// anchors, and right-to-left only flips `inline-*` sides; add them from Base UI's useAnchorPositioning when a component needs them
export function usePosition({
  anchor,
  side,
  align,
  sideOffset,
  alignOffset,
  collisionPadding = 5,
}: {
  anchor: () => Element | null
  side: Side
  align: Align
  sideOffset: number
  alignOffset: number
  collisionPadding?: number
}) {
  const [element, setElement] = useState<HTMLElement | null>(null)
  const [placed, setPlaced] = useState<Placed | null>(null)
  const [lockedSide, setLockedSide] = useState<PhysicalSide | null>(null)
  const [anchorElement, setAnchorElement] = useState<Element | null>(null)

  const rtl = !!anchorElement && getComputedStyle(anchorElement).direction === "rtl"
  const physical = (s: Side): PhysicalSide => (s === "inline-end" ? (rtl ? "left" : "right") : s === "inline-start" ? (rtl ? "right" : "left") : s)
  const requested = physical(side)
  const placement = (lockedSide ?? requested) + (align === "center" ? "" : `-${align}`)

  // The anchor can be a ref that is only filled once the page has mounted
  useLayoutEffect(() => {
    if (element) setAnchorElement(anchor())
  })

  useLayoutEffect(() => {
    if (element) return
    setPlaced(null)
    setLockedSide(null)
  }, [element])

  useLayoutEffect(() => {
    if (!element || !anchorElement) return
    let active = true
    const padding = { top: collisionPadding, right: collisionPadding, bottom: collisionPadding, left: collisionPadding }
    // Biased towards the requested side, so a popup that fits exactly does not flip
    const flipPadding = {
      top: padding.top + 1 + (requested === "bottom" ? 1 : 0),
      bottom: padding.bottom + 1 + (requested === "top" ? 1 : 0),
      left: padding.left + 1 + (requested === "right" ? 1 : 0),
      right: padding.right + 1 + (requested === "left" ? 1 : 0),
    }
    let reference = { x: 0, y: 0, width: 0, height: 0 }
    let floating = { width: 0, height: 0 }
    let available = { width: 0, height: 0 }
    const measure: Middleware = {
      name: "measure",
      fn: ({ rects }) => {
        reference = rects.reference
        floating = rects.floating
        return {}
      },
    }
    const shiftMiddleware = shift({ padding, mainAxis: true, crossAxis: false, limiter: limitShift() })
    const flipMiddleware = flip({ padding: flipPadding, mainAxis: true, crossAxis: "alignment", fallbackAxisSideDirection: "none" })
    const update = () =>
      computePosition(anchorElement, element, {
        strategy: "absolute",
        placement: placement as Placement,
        middleware: [
          offset({ mainAxis: sideOffset, crossAxis: alignOffset, alignmentAxis: alignOffset }),
          ...(align === "center" ? [shiftMiddleware, flipMiddleware] : [flipMiddleware, shiftMiddleware]),
          size({
            padding,
            apply: ({ availableWidth, availableHeight }) => void (available = { width: availableWidth, height: availableHeight }),
          }),
          measure,
        ],
      }).then(({ x, y, placement: result, middlewareData }) => {
        if (!active) return
        const [renderedSide, renderedAlign = "center"] = result.split("-") as [PhysicalSide, Align?]
        const vertical = renderedSide === "top" || renderedSide === "bottom"
        // An aligned popup grows from its aligned edge until a shift breaks the alignment, otherwise from the anchor's center
        const aligned = renderedAlign !== "center" && Math.abs((vertical ? middlewareData.shift?.x : middlewareData.shift?.y) ?? 0) <= 1
        const center = vertical ? reference.x + reference.width / 2 - x : reference.y + reference.height / 2 - y
        const cross = aligned ? (renderedAlign === "start" ? "0%" : "100%") : `${Math.min(Math.max(center, 0), vertical ? floating.width : floating.height)}px`
        const sideOrigin = renderedSide === "top" || renderedSide === "left" ? `calc(100% + ${sideOffset}px)` : `${-sideOffset}px`
        const ratio = window.devicePixelRatio || 1
        // Snapped to device pixels so the popup is exactly as wide as the anchor looks
        const snap = (start: number, length: number) => `${(Math.round((start + length) * ratio) - Math.round(start * ratio)) / ratio}px`
        const next: Placed = {
          x: Math.round(x * ratio) / ratio,
          y: Math.round(y * ratio) / ratio,
          side: renderedSide,
          align: renderedAlign,
          vars: {
            "--available-width": `${available.width}px`,
            "--available-height": `${available.height}px`,
            "--anchor-width": snap(reference.x, reference.width),
            "--anchor-height": snap(reference.y, reference.height),
            "--transform-origin": vertical ? `${cross} ${sideOrigin}` : `${sideOrigin} ${cross}`,
          },
        }
        setPlaced((previous) =>
          previous && previous.x === next.x && previous.y === next.y && previous.side === next.side && previous.align === next.align && Object.keys(next.vars).every((k) => previous.vars[k] === next.vars[k])
            ? previous
            : next
        )
        // Once flipped, keep the side while filtering resizes the popup
        setLockedSide((locked) => (locked ?? (renderedSide !== requested ? renderedSide : null)))
      })
    const stop = autoUpdate(anchorElement, element, update)
    return () => {
      active = false
      stop()
    }
  }, [element, anchorElement, placement, requested, align, sideOffset, alignOffset, collisionPadding])

  // The seeded sizes keep `max-height: min(x, var(--available-height))` valid until the first measurement
  const style: Record<string, string | number> = placed
    ? { position: "absolute", top: 0, left: 0, transform: `translate(${placed.x}px, ${placed.y}px)`, ...placed.vars }
    : { position: "fixed", top: 0, left: 0, opacity: 0, "--available-width": "100vw", "--available-height": "100vh" }

  const logicalSide: Side = placed ? (side === "inline-end" || side === "inline-start" ? (placed.side === "left" || placed.side === "right" ? (placed.side === (rtl ? "left" : "right") ? "inline-end" : "inline-start") : placed.side) : placed.side) : side
  return { setElement, style, positioned: !!placed, side: logicalSide, align: placed?.align ?? align }
}
