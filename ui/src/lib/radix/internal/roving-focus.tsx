import { useCallback, useEffect, useId, useRef, useState } from "preact/hooks"
import { useDirection, type Direction } from "../direction"
import { createCollection } from "./collection"
import { composeEventHandlers } from "./compose-event-handlers"
import { createStrictContext } from "./create-context"
import { forwardRef } from "./forward-ref"
import { Primitive, type PrimitiveWith } from "./primitive"
import { mergeStyles } from "./style"

type Orientation = "horizontal" | "vertical"
type FocusIntent = "first" | "last" | "prev" | "next"

const MAP_KEY_TO_FOCUS_INTENT: Record<string, FocusIntent> = {
  ArrowLeft: "prev",
  ArrowUp: "prev",
  ArrowRight: "next",
  ArrowDown: "next",
  PageUp: "first",
  Home: "first",
  PageDown: "last",
  End: "last",
}

const Collection = createCollection<{ id: string; focusable: boolean; active: boolean }>()

const [RovingFocusProvider, useRovingFocusContext] = createStrictContext<{
  orientation?: Orientation
  dir: Direction
  loop: boolean
  currentTabStopId: string | null
  onItemFocus(tabStopId: string): void
  onItemShiftTab(): void
  onFocusableItemAdd(): void
  onFocusableItemRemove(): void
}>("RovingFocusGroup")

function getDirectionAwareKey(key: string, dir?: Direction) {
  if (dir !== "rtl") return key
  return key === "ArrowLeft" ? "ArrowRight" : key === "ArrowRight" ? "ArrowLeft" : key
}

function getFocusIntent(event: KeyboardEvent, orientation?: Orientation, dir?: Direction) {
  const key = getDirectionAwareKey(event.key, dir)
  if (orientation === "vertical" && ["ArrowLeft", "ArrowRight"].includes(key)) return undefined
  if (orientation === "horizontal" && ["ArrowUp", "ArrowDown"].includes(key)) return undefined
  return MAP_KEY_TO_FOCUS_INTENT[key]
}

function focusFirst(candidates: HTMLElement[]) {
  const previouslyFocusedElement = document.activeElement
  for (const candidate of candidates) {
    if (candidate === previouslyFocusedElement) return
    candidate.focus()
    if (document.activeElement !== previouslyFocusedElement) return
  }
}

function wrapArray<T>(array: T[], startIndex: number) {
  return array.map((_, index) => array[(startIndex + index) % array.length]!)
}

type RovingFocusGroupProps = PrimitiveWith<
  "div",
  { orientation?: Orientation; dir?: Direction; loop?: boolean }
>

const RovingFocusGroupImpl = forwardRef<RovingFocusGroupProps>(({ orientation, dir, loop = false, ...groupProps }, ref) => {
  const direction = useDirection(dir)
  const [currentTabStopId, setCurrentTabStopId] = useState<string | null>(null)
  const [isTabbingBackOut, setIsTabbingBackOut] = useState(false)
  const getItems = Collection.useCollection()
  const isClickFocusRef = useRef(false)
  const [focusableItemsCount, setFocusableItemsCount] = useState(0)

  return (
    <RovingFocusProvider
      value={{
        orientation,
        dir: direction,
        loop,
        currentTabStopId,
        onItemFocus: setCurrentTabStopId,
        onItemShiftTab: useCallback(() => setIsTabbingBackOut(true), []),
        onFocusableItemAdd: useCallback(() => setFocusableItemsCount((count) => count + 1), []),
        onFocusableItemRemove: useCallback(() => setFocusableItemsCount((count) => count - 1), []),
      }}
    >
      <Primitive.div
        tabIndex={isTabbingBackOut || focusableItemsCount === 0 ? -1 : 0}
        data-orientation={orientation}
        {...groupProps}
        ref={ref}
        style={mergeStyles({ outline: "none" }, groupProps.style)}
        onMouseDown={composeEventHandlers(groupProps.onMouseDown, () => {
          isClickFocusRef.current = true
        })}
        onFocusIn={composeEventHandlers(groupProps.onFocusIn, (event) => {
          // `focus` does not bubble in Preact, `focusin` does, which mirrors React's onFocus
          const isKeyboardFocus = !isClickFocusRef.current
          if (event.target === event.currentTarget && isKeyboardFocus && !isTabbingBackOut) {
            const items = getItems().filter((item) => item.focusable)
            const activeItem = items.find((item) => item.active)
            const currentItem = items.find((item) => item.id === currentTabStopId)
            focusFirst([activeItem, currentItem, ...items].filter(Boolean).map((item) => item!.ref.current!))
          }
          isClickFocusRef.current = false
        })}
        onFocusOut={composeEventHandlers(groupProps.onFocusOut, () => setIsTabbingBackOut(false))}
      />
    </RovingFocusProvider>
  )
})

const RovingFocusGroup = forwardRef<RovingFocusGroupProps>((props, ref) => (
  <Collection.Provider>
    <Collection.Slot>
      <RovingFocusGroupImpl {...props} ref={ref} />
    </Collection.Slot>
  </Collection.Provider>
))

type RovingFocusItemProps = PrimitiveWith<"span", { focusable?: boolean; active?: boolean }>

const RovingFocusGroupItem = forwardRef<RovingFocusItemProps>(
  ({ focusable = true, active = false, ...itemProps }, forwardedRef) => {
    const id = useId()
    const context = useRovingFocusContext("RovingFocusGroupItem")
    const getItems = Collection.useCollection()
    const isCurrentTabStop = context.currentTabStopId === id
    const { onFocusableItemAdd, onFocusableItemRemove } = context

    useEffect(() => {
      if (!focusable) return
      onFocusableItemAdd()
      return () => onFocusableItemRemove()
    }, [focusable, onFocusableItemAdd, onFocusableItemRemove])

    return (
      <Collection.ItemSlot id={id} focusable={focusable} active={active}>
        <Primitive.span
          tabIndex={isCurrentTabStop ? 0 : -1}
          data-orientation={context.orientation}
          {...itemProps}
          ref={forwardedRef}
          onMouseDown={composeEventHandlers(itemProps.onMouseDown, (event) => {
            // Non-focusable items must not take focus on mousedown even though tabIndex is -1
            if (!focusable) event.preventDefault()
            // Safari doesn't focus a button when clicked so we run our logic on mousedown also
            else context.onItemFocus(id)
          })}
          onFocus={composeEventHandlers(itemProps.onFocus, () => context.onItemFocus(id))}
          onKeyDown={composeEventHandlers(itemProps.onKeyDown, (event) => {
            if (event.key === "Tab" && event.shiftKey) {
              context.onItemShiftTab()
              return
            }
            if (event.target !== event.currentTarget) return

            const focusIntent = getFocusIntent(event, context.orientation, context.dir)
            if (focusIntent === undefined) return
            if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return
            event.preventDefault()

            let candidateNodes = getItems().filter((item) => item.focusable).map((item) => item.ref.current!)

            if (focusIntent === "last") candidateNodes.reverse()
            else if (focusIntent === "prev" || focusIntent === "next") {
              if (focusIntent === "prev") candidateNodes.reverse()
              const currentIndex = candidateNodes.indexOf(event.currentTarget)
              candidateNodes = context.loop ? wrapArray(candidateNodes, currentIndex + 1) : candidateNodes.slice(currentIndex + 1)
            }

            // Let the keydown finish before moving focus so the next item does not receive the same key event
            setTimeout(() => focusFirst(candidateNodes))
          })}
        />
      </Collection.ItemSlot>
    )
  }
)

export { RovingFocusGroup as Root, RovingFocusGroupItem as Item }
