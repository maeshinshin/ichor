import { Component, createContext, render, type ComponentChildren, type Ref } from "preact"
import { useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from "preact/hooks"
import { useDirection, type Direction } from "./direction"
import { createCollection } from "./internal/collection"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { useDismissableLayer, type DismissableLayerProps } from "./internal/dismissable-layer"
import { useFocusScope } from "./internal/focus-scope"
import { forwardRef } from "./internal/forward-ref"
import { useModalLayer } from "./internal/modal-layer"
import * as PopperPrimitive from "./internal/popper"
import { ContextProvider, Root as PortalRoot } from "./internal/portal"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { mergeStyles } from "./internal/style"
import { getNextMatch, useTypeahead } from "./internal/typeahead"
import { useControllableState } from "./internal/use-controllable-state"
import { VisuallyHidden } from "./internal/visually-hidden"

const OPEN_KEYS = [" ", "Enter", "ArrowUp", "ArrowDown"]
const SELECTION_KEYS = [" ", "Enter"]
// The space kept between the list and the edges of the viewport
const CONTENT_MARGIN = 10

type Position = "item-aligned" | "popper"
type Item = { value: string; disabled: boolean; textValue: string }

const Collection = createCollection<Item>()

const shouldShowPlaceholder = (value?: string) => value === "" || value === undefined
const clamp = (value: number, [min, max]: [number, number]) => Math.min(max, Math.max(min, value))

const findNextItem = <T extends { textValue: string }>(items: T[], search: string, currentItem?: T) => {
  const nextMatch = getNextMatch(items.map((item) => item.textValue), search, currentItem?.textValue)
  return items.find((item) => item.textValue === nextMatch)
}

const [SelectProvider, useSelectContext] = createStrictContext<{
  required?: boolean
  name?: string
  form?: string
  disabled?: boolean
  trigger: HTMLButtonElement | null
  onTriggerChange(trigger: HTMLButtonElement | null): void
  valueNode: HTMLElement | null
  onValueNodeChange(node: HTMLElement | null): void
  contentId: string
  value: string | undefined
  onValueChange(value: string): void
  open: boolean
  onOpenChange(open: boolean): void
  dir: Direction
  // Where a pointer press on the trigger started, so releasing after dragging over the list can select
  triggerPointerDownPosRef: { current: { x: number; y: number } | null }
}>("Select")

// The selected item's text, handed to the trigger's value. Kept apart from the main context so a change re-renders only its readers.
const [ValueContentProvider, useValueContent] = createStrictContext<{
  content: { value: string; children: ComponentChildren } | null
  onContentChange(content: { value: string; children: ComponentChildren }): void
}>("Select")

function ValueContentHost({ children }: { children?: ComponentChildren }) {
  const [content, onContentChange] = useState<{ value: string; children: ComponentChildren } | null>(null)
  return <ValueContentProvider value={{ content, onContentChange }}>{children}</ValueContentProvider>
}

type ContentContextValue = {
  content?: HTMLElement | null
  viewport?: HTMLElement | null
  onViewportChange?(node: HTMLElement | null): void
  itemRefCallback?(node: HTMLElement | null, value: string, disabled: boolean): void
  selectedItem?: HTMLElement | null
  onItemLeave?(): void
  itemTextRefCallback?(node: HTMLElement | null, value: string, disabled: boolean): void
  focusSelectedItem?(): void
  selectedItemText?: HTMLElement | null
  position?: Position
  isPositioned?: boolean
  searchRef?: { current: string }
}
const [SelectContentProvider, useSelectContentContext] = createStrictContext<ContentContextValue>("SelectContent")
// While closed the parts render without a list around them
const CLOSED_CONTENT: ContentContextValue = {}

type ViewportContextValue = {
  contentWrapper?: HTMLElement | null
  shouldExpandOnScrollRef?: { current: boolean }
  onScrollButtonChange?(node: HTMLElement | null): void
}
const ViewportContext = createContext<ViewportContextValue>({})

const [SelectGroupProvider, useSelectGroupContext] = createStrictContext<{ id: string }>("SelectGroup")
const [SelectItemProvider, useSelectItemContext] = createStrictContext<{
  value: string
  disabled: boolean
  textId: string
  isSelected: boolean
  onItemTextChange(node: HTMLElement | null): void
}>("SelectItem")

function Select({
  open: openProp,
  defaultOpen,
  onOpenChange,
  value: valueProp,
  defaultValue,
  onValueChange,
  dir,
  name,
  disabled,
  required,
  form,
  children,
}: {
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?(open: boolean): void
  value?: string
  defaultValue?: string
  onValueChange?(value: string): void
  dir?: Direction
  name?: string
  disabled?: boolean
  required?: boolean
  form?: string
  children?: ComponentChildren
}) {
  const [trigger, setTrigger] = useState<HTMLButtonElement | null>(null)
  const [valueNode, setValueNode] = useState<HTMLElement | null>(null)
  const direction = useDirection(dir)
  const [open, setOpen] = useControllableState({ prop: openProp, defaultProp: defaultOpen ?? false, onChange: onOpenChange })
  const [value, setValue] = useControllableState<string | undefined>({
    prop: valueProp,
    defaultProp: defaultValue,
    onChange: onValueChange as (value: string | undefined) => void,
  })
  const triggerPointerDownPosRef = useRef<{ x: number; y: number } | null>(null)
  const initialValueRef = useRef(value)

  // A form reset puts the value back to what it started as
  useEffect(() => {
    const associatedForm = form ? trigger?.ownerDocument.getElementById(form) : trigger?.form
    if (associatedForm instanceof HTMLFormElement) {
      const reset = () => setValue(initialValueRef.current)
      associatedForm.addEventListener("reset", reset)
      return () => associatedForm.removeEventListener("reset", reset)
    }
  }, [form, trigger, setValue])

  // Before the trigger mounts (and on the server) assume a form, so server-rendered forms submit without JavaScript
  const isFormControl = trigger ? !!form || !!trigger.closest("form") : true

  return (
    <PopperPrimitive.Root>
      <SelectProvider
        value={{
          required,
          name,
          form,
          disabled,
          trigger,
          onTriggerChange: setTrigger,
          valueNode,
          onValueNodeChange: setValueNode,
          contentId: useId(),
          value,
          onValueChange: setValue,
          open,
          onOpenChange: setOpen,
          dir: direction,
          triggerPointerDownPosRef,
        }}
      >
        <Collection.Provider>
          <ValueContentHost>
            {children}
            {isFormControl && <NativeSelect />}
          </ValueContentHost>
        </Collection.Provider>
      </SelectProvider>
    </PopperPrimitive.Root>
  )
}

// shortcut: the hidden native select holds only the empty option and the selected one, so a form submits and validates but the
// browser cannot autofill it (no `autoComplete`); mirror every item as an option if autofill matters
function NativeSelect() {
  const context = useSelectContext("Select")
  const ref = useRef<HTMLSelectElement | null>(null)
  const value = context.value ?? ""
  const previous = useRef(value)

  // Announce changes to the form, as a native select would
  useEffect(() => {
    if (previous.current === value) return
    previous.current = value
    ref.current?.dispatchEvent(new Event("change", { bubbles: true }))
  }, [value])

  return (
    <VisuallyHidden asChild>
      <select aria-hidden="true" tabIndex={-1} required={context.required} name={context.name} disabled={context.disabled} form={context.form} value={value} ref={ref}>
        <option value="" />
        {value !== "" && <option value={value} />}
      </select>
    </VisuallyHidden>
  )
}

const SelectTrigger = forwardRef<PrimitiveWith<"button", { disabled?: boolean }>>(({ disabled = false, ...triggerProps }, forwardedRef) => {
  const context = useSelectContext("SelectTrigger")
  const isDisabled = context.disabled || disabled
  const composedRefs = useComposedRefs(forwardedRef, context.onTriggerChange)
  const getItems = Collection.useCollection()
  const pointerTypeRef = useRef("touch")

  const typeahead = useTypeahead((search) => {
    const enabledItems = getItems().filter((item) => !item.disabled)
    const nextItem = findNextItem(enabledItems, search, enabledItems.find((item) => item.value === context.value))
    if (nextItem) context.onValueChange(nextItem.value)
  })

  const handleOpen = (pointerEvent?: MouseEvent) => {
    if (!isDisabled) {
      context.onOpenChange(true)
      typeahead.reset()
    }
    if (pointerEvent) context.triggerPointerDownPosRef.current = { x: Math.round(pointerEvent.pageX), y: Math.round(pointerEvent.pageY) }
  }

  return (
    <PopperPrimitive.Anchor asChild>
      <Primitive.button
        type="button"
        role="combobox"
        aria-controls={context.open ? context.contentId : undefined}
        aria-expanded={context.open}
        aria-required={context.required}
        aria-autocomplete="none"
        dir={context.dir}
        data-state={context.open ? "open" : "closed"}
        disabled={isDisabled}
        data-disabled={isDisabled ? "" : undefined}
        data-placeholder={shouldShowPlaceholder(context.value) ? "" : undefined}
        {...triggerProps}
        ref={composedRefs}
        // Keeps a label's click working in Safari, which would not focus the button on its own
        onClick={composeEventHandlers(triggerProps.onClick, (event) => {
          event.currentTarget.focus()
          // A mouse opened it on press; touch and pen open on click so a scroll gesture does not
          if (pointerTypeRef.current !== "mouse") handleOpen(event)
        })}
        onPointerDown={composeEventHandlers(triggerProps.onPointerDown, (event) => {
          pointerTypeRef.current = event.pointerType
          // Implicit pointer capture would hide the pointer from the list the press may be dragged into
          const target = event.target as HTMLElement
          if (target.hasPointerCapture(event.pointerId)) target.releasePointerCapture(event.pointerId)
          if (event.button === 0 && event.ctrlKey === false && event.pointerType === "mouse") {
            handleOpen(event)
            // Keeps the trigger from stealing focus from the item focused after opening
            event.preventDefault()
          }
        })}
        onKeyDown={composeEventHandlers(triggerProps.onKeyDown, (event) => {
          const isTypingAhead = typeahead.search.current !== ""
          if (!event.ctrlKey && !event.altKey && !event.metaKey && event.key.length === 1) typeahead.type(event.key)
          if (isTypingAhead && event.key === " ") {
            // Space belongs to the search: it must not click the trigger when released either
            event.preventDefault()
            return
          }
          if (OPEN_KEYS.includes(event.key)) {
            handleOpen()
            event.preventDefault()
          }
        })}
      />
    </PopperPrimitive.Anchor>
  )
})

// className and style are dropped: this part is not meant to be styled, its parent is
const SelectValue = forwardRef<PrimitiveWith<"span", { placeholder?: ComponentChildren }>>(
  ({ className, style, children, placeholder = "", ...valueProps }, forwardedRef) => {
    const context = useSelectContext("SelectValue")
    const valueContent = useValueContent("SelectValue")
    const composedRefs = useComposedRefs(forwardedRef, context.onValueNodeChange)
    const showPlaceholder = shouldShowPlaceholder(context.value)
    // The text of the selected item is what the list shows for it
    const { content } = valueContent
    const selected = content && content.value === context.value ? content.children : null
    return (
      <Primitive.span {...valueProps} asChild={showPlaceholder ? false : valueProps.asChild} ref={composedRefs} style={{ pointerEvents: "none" }}>
        {showPlaceholder ? placeholder : (children ?? selected)}
      </Primitive.span>
    )
  }
)

const SelectIcon = forwardRef<PrimitiveProps<"span">>(({ children, ...iconProps }, forwardedRef) => (
  <Primitive.span aria-hidden="true" {...iconProps} ref={forwardedRef}>
    {children || "▼"}
  </Primitive.span>
))

function SelectPortal({ children }: { children?: ComponentChildren }) {
  return <PortalRoot>{children}</PortalRoot>
}

// The items of a closed list have to exist so the trigger can show the selected one and find items by typing. They render into a
// fragment that is never attached to the page (Radix does the same through a portal), and see this tree's context.
class Detached extends Component<{ children?: ComponentChildren }> {
  fragment?: DocumentFragment
  override componentDidMount() {
    this.fragment = document.createDocumentFragment()
    this.renderChildren()
  }
  override componentDidUpdate() {
    this.renderChildren()
  }
  override componentWillUnmount() {
    render(null, this.fragment!)
  }
  renderChildren() {
    render(<ContextProvider context={this.context}>{this.props.children}</ContextProvider>, this.fragment!)
  }
  render() {
    return null
  }
}

// shortcut: no `forceMount`, Portal `container`, `avoidCollisions`, `collisionBoundary`, `sticky`, `hideWhenDetached` or `arrowPadding`;
// add them if a list must stay mounted or be tuned against collisions
type SelectContentProps = PopperPrimitive.PopperContentProps &
  Pick<DismissableLayerProps, "onEscapeKeyDown" | "onPointerDownOutside"> & {
    position?: Position
    onCloseAutoFocus?(event: Event): void
  }

const SelectContent = forwardRef<SelectContentProps>((props, forwardedRef) => {
  const context = useSelectContext("SelectContent")
  return (
    <Presence present={context.open}>
      {({ present }) => (present ? <SelectContentImpl {...props} ref={forwardedRef} /> : <SelectContentFragment>{props.children}</SelectContentFragment>)}
    </Presence>
  )
})

const SelectContentFragment = forwardRef<{ children?: ComponentChildren; ref?: Ref<any> }>(({ children }, forwardedRef) => (
  <Detached>
    <SelectContentProvider value={CLOSED_CONTENT}>
      <Collection.Slot>
        <div ref={forwardedRef}>{children}</div>
      </Collection.Slot>
    </SelectContentProvider>
  </Detached>
))

const SelectContentImpl = forwardRef<SelectContentProps>(
  (
    {
      position = "item-aligned",
      onCloseAutoFocus,
      onEscapeKeyDown,
      onPointerDownOutside,
      // Popper placement props, which the item-aligned position ignores
      side,
      sideOffset,
      align,
      alignOffset,
      collisionPadding,
      ...contentProps
    },
    forwardedRef
  ) => {
    const context = useSelectContext("SelectContent")
    const [content, setContent] = useState<HTMLElement | null>(null)
    const [viewport, setViewport] = useState<HTMLElement | null>(null)
    const composedRefs = useComposedRefs(forwardedRef, setContent)
    const [selectedItem, setSelectedItem] = useState<HTMLElement | null>(null)
    const [selectedItemText, setSelectedItemText] = useState<HTMLElement | null>(null)
    const getItems = Collection.useCollection()
    const [isPositioned, setIsPositioned] = useState(false)
    const firstValidItemFoundRef = useRef(false)
    const firstItemFoundRef = useRef(false)
    const firstItemTextFoundRef = useRef(false)
    const { onOpenChange, triggerPointerDownPosRef } = context

    // Registered before the scope moves focus, so a layer below does not take that focus for one leaving it
    useDismissableLayer(content, {
      onEscapeKeyDown,
      onPointerDownOutside,
      // The page behind is out of reach, so a focus change cannot mean the user left
      onFocusOutside: (event) => event.preventDefault(),
      onDismiss: () => onOpenChange(false),
    })
    useFocusScope(content, {
      // The selected item takes focus once the list is placed
      onMountAutoFocus: (event) => event.preventDefault(),
      onUnmountAutoFocus: composeEventHandlers(onCloseAutoFocus, (event) => {
        context.trigger?.focus({ preventScroll: true })
        event.preventDefault()
      }),
    })
    // A list that is animating out no longer holds on to the page
    useModalLayer(context.open ? content : null)

    const focusFirst = useCallback(
      (candidates: Array<HTMLElement | null>) => {
        const items = getItems().map((item) => item.ref.current)
        const firstItem = items[0]
        const lastItem = items[items.length - 1]
        const previouslyFocused = document.activeElement
        for (const candidate of candidates) {
          // Focus is already where we want to go
          if (candidate === previouslyFocused) return
          candidate?.scrollIntoView({ block: "nearest" })
          // The viewport may have padding, so scroll to its edges when focusing the first or last item
          if (candidate === firstItem && viewport) viewport.scrollTop = 0
          if (candidate === lastItem && viewport) viewport.scrollTop = viewport.scrollHeight
          candidate?.focus()
          if (document.activeElement !== previouslyFocused) return
        }
      },
      [getItems, viewport]
    )
    const focusSelectedItem = useCallback(() => focusFirst([selectedItem, content]), [focusFirst, selectedItem, content])

    useEffect(() => {
      if (isPositioned) focusSelectedItem()
    }, [isPositioned, focusSelectedItem])

    // Pressing on the trigger, dragging over the list and releasing selects, but releasing without moving must not select the item
    // that appeared under the pointer
    useEffect(() => {
      if (!content) return
      let pointerMoveDelta = { x: 0, y: 0 }
      const handlePointerMove = (event: PointerEvent) => {
        pointerMoveDelta = {
          x: Math.abs(Math.round(event.pageX) - (triggerPointerDownPosRef.current?.x ?? 0)),
          y: Math.abs(Math.round(event.pageY) - (triggerPointerDownPosRef.current?.y ?? 0)),
        }
      }
      const handlePointerUp = (event: PointerEvent) => {
        if (pointerMoveDelta.x <= 10 && pointerMoveDelta.y <= 10) event.preventDefault()
        // Otherwise, a release outside the list closes it
        else if (!event.composedPath().includes(content)) onOpenChange(false)
        document.removeEventListener("pointermove", handlePointerMove)
        triggerPointerDownPosRef.current = null
      }
      if (triggerPointerDownPosRef.current !== null) {
        document.addEventListener("pointermove", handlePointerMove)
        document.addEventListener("pointerup", handlePointerUp, { capture: true, once: true })
      }
      return () => {
        document.removeEventListener("pointermove", handlePointerMove)
        document.removeEventListener("pointerup", handlePointerUp, { capture: true })
      }
    }, [content, onOpenChange, triggerPointerDownPosRef])

    useEffect(() => {
      const close = () => onOpenChange(false)
      window.addEventListener("blur", close)
      window.addEventListener("resize", close)
      return () => {
        window.removeEventListener("blur", close)
        window.removeEventListener("resize", close)
      }
    }, [onOpenChange])

    const typeahead = useTypeahead((search) => {
      const enabledItems = getItems().filter((item) => !item.disabled)
      const nextItem = findNextItem(enabledItems, search, enabledItems.find((item) => item.ref.current === document.activeElement))
      nextItem?.ref.current?.focus()
    })

    // The item the list opens on (and aligns to the trigger) is the selected one, else the first that can be selected
    const itemRefCallback = useCallback(
      (node: HTMLElement | null, value: string, disabled: boolean) => {
        const isFirstItem = !firstItemFoundRef.current
        if (isFirstItem) firstItemFoundRef.current = true
        const isFirstValidItem = !firstValidItemFoundRef.current && !disabled
        const isSelectedItem = context.value !== undefined && context.value === value
        if (isSelectedItem || isFirstValidItem) {
          setSelectedItem(node)
          if (isFirstValidItem) firstValidItemFoundRef.current = true
        } else if (isFirstItem) setSelectedItem(node)
      },
      [context.value]
    )
    const itemTextRefCallback = useCallback(
      (node: HTMLElement | null, value: string, disabled: boolean) => {
        if (!node) return
        const isFirstItem = !firstItemTextFoundRef.current
        if (isFirstItem) firstItemTextFoundRef.current = true
        const isFirstValidItem = !firstValidItemFoundRef.current && !disabled
        const isSelectedItem = context.value !== undefined && context.value === value
        if (isSelectedItem || isFirstValidItem || isFirstItem) setSelectedItemText(node)
      },
      [context.value]
    )
    const handleItemLeave = useCallback(() => content?.focus(), [content])

    const SelectPosition = position === "popper" ? SelectPopperPosition : SelectItemAlignedPosition
    const popperContentProps = position === "popper" ? { side, sideOffset, align, alignOffset, collisionPadding } : {}

    return (
      <SelectContentProvider
        value={{
          content,
          viewport,
          onViewportChange: setViewport,
          itemRefCallback,
          selectedItem,
          onItemLeave: handleItemLeave,
          itemTextRefCallback,
          focusSelectedItem,
          selectedItemText,
          position,
          isPositioned,
          searchRef: typeahead.search,
        }}
      >
        <SelectPosition
          role="listbox"
          id={context.contentId}
          data-state={context.open ? "open" : "closed"}
          dir={context.dir}
          tabIndex={-1}
          onContextMenu={(event) => event.preventDefault()}
          {...contentProps}
          {...popperContentProps}
          onPlaced={() => setIsPositioned(true)}
          ref={composedRefs}
          // Flex layout places the scroll buttons, and the list itself may take focus
          style={mergeStyles({ display: "flex", flexDirection: "column", outline: "none" }, contentProps.style)}
          onKeyDown={composeEventHandlers(contentProps.onKeyDown, (event) => {
            // Lists are not navigated with Tab
            if (event.key === "Tab") event.preventDefault()
            if (!event.ctrlKey && !event.altKey && !event.metaKey && event.key.length === 1) typeahead.type(event.key)
            if (["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) {
              let candidateNodes = getItems()
                .filter((item) => !item.disabled)
                .map((item) => item.ref.current!)
              if (["ArrowUp", "End"].includes(event.key)) candidateNodes = candidateNodes.slice().reverse()
              if (["ArrowUp", "ArrowDown"].includes(event.key)) candidateNodes = candidateNodes.slice(candidateNodes.indexOf(event.target as HTMLElement) + 1)
              focusFirst(candidateNodes)
              event.preventDefault()
            }
          })}
        />
      </SelectContentProvider>
    )
  }
)

type PositionProps = PopperPrimitive.PopperContentProps

// Opens the list over the trigger with the selected item on top of the value, like a native select, within the viewport
const SelectItemAlignedPosition = forwardRef<PositionProps>(({ onPlaced, ...popperProps }, forwardedRef) => {
  const context = useSelectContext("SelectContent")
  const contentContext = useSelectContentContext("SelectContent")
  const [contentWrapper, setContentWrapper] = useState<HTMLDivElement | null>(null)
  const [content, setContent] = useState<HTMLElement | null>(null)
  const composedRefs = useComposedRefs(forwardedRef, setContent)
  const shouldExpandOnScrollRef = useRef(false)
  const shouldRepositionRef = useRef(true)
  const { viewport, selectedItem, selectedItemText, focusSelectedItem } = contentContext

  const position = useCallback(() => {
    if (!(context.trigger && context.valueNode && contentWrapper && content && viewport && selectedItem && selectedItemText)) return
    const triggerRect = context.trigger.getBoundingClientRect()

    // Horizontal positioning
    const contentRect = content.getBoundingClientRect()
    const valueNodeRect = context.valueNode.getBoundingClientRect()
    const itemTextRect = selectedItemText.getBoundingClientRect()

    if (context.dir !== "rtl") {
      const itemTextOffset = itemTextRect.left - contentRect.left
      const left = valueNodeRect.left - itemTextOffset
      const leftDelta = triggerRect.left - left
      const minContentWidth = triggerRect.width + leftDelta
      const contentWidth = Math.max(minContentWidth, contentRect.width)
      const rightEdge = window.innerWidth - CONTENT_MARGIN
      // Keeps the list from leaving through the starting edge; the ending edge is the page's to manage
      const clampedLeft = clamp(left, [CONTENT_MARGIN, Math.max(CONTENT_MARGIN, rightEdge - contentWidth)])
      contentWrapper.style.minWidth = minContentWidth + "px"
      contentWrapper.style.left = clampedLeft + "px"
    } else {
      const itemTextOffset = contentRect.right - itemTextRect.right
      const right = window.innerWidth - valueNodeRect.right - itemTextOffset
      const rightDelta = window.innerWidth - triggerRect.right - right
      const minContentWidth = triggerRect.width + rightDelta
      const contentWidth = Math.max(minContentWidth, contentRect.width)
      const leftEdge = window.innerWidth - CONTENT_MARGIN
      const clampedRight = clamp(right, [CONTENT_MARGIN, Math.max(CONTENT_MARGIN, leftEdge - contentWidth)])
      contentWrapper.style.minWidth = minContentWidth + "px"
      contentWrapper.style.right = clampedRight + "px"
    }

    // Vertical positioning
    const items = Array.from(viewport.querySelectorAll("[data-radix-collection-item]"))
    const availableHeight = window.innerHeight - CONTENT_MARGIN * 2
    const itemsHeight = viewport.scrollHeight

    const contentStyles = window.getComputedStyle(content)
    const contentBorderTopWidth = parseInt(contentStyles.borderTopWidth, 10)
    const contentPaddingTop = parseInt(contentStyles.paddingTop, 10)
    const contentBorderBottomWidth = parseInt(contentStyles.borderBottomWidth, 10)
    const contentPaddingBottom = parseInt(contentStyles.paddingBottom, 10)
    const fullContentHeight = contentBorderTopWidth + contentPaddingTop + itemsHeight + contentPaddingBottom + contentBorderBottomWidth
    const minContentHeight = Math.min(selectedItem.offsetHeight * 5, fullContentHeight)

    const viewportStyles = window.getComputedStyle(viewport)
    const viewportPaddingTop = parseInt(viewportStyles.paddingTop, 10)
    const viewportPaddingBottom = parseInt(viewportStyles.paddingBottom, 10)

    const topEdgeToTriggerMiddle = triggerRect.top + triggerRect.height / 2 - CONTENT_MARGIN
    const triggerMiddleToBottomEdge = availableHeight - topEdgeToTriggerMiddle

    const selectedItemHalfHeight = selectedItem.offsetHeight / 2
    const itemOffsetMiddle = selectedItem.offsetTop + selectedItemHalfHeight
    const contentTopToItemMiddle = contentBorderTopWidth + contentPaddingTop + itemOffsetMiddle
    const itemMiddleToContentBottom = fullContentHeight - contentTopToItemMiddle

    const willAlignWithoutTopOverflow = contentTopToItemMiddle <= topEdgeToTriggerMiddle

    if (willAlignWithoutTopOverflow) {
      const isLastItem = items.length > 0 && selectedItem === items[items.length - 1]
      contentWrapper.style.bottom = "0px"
      const viewportOffsetBottom = content.clientHeight - viewport.offsetTop - viewport.offsetHeight
      // The viewport may have padding at the bottom, which counts to avoid a scrollable viewport
      const clampedTriggerMiddleToBottomEdge = Math.max(
        triggerMiddleToBottomEdge,
        selectedItemHalfHeight + (isLastItem ? viewportPaddingBottom : 0) + viewportOffsetBottom + contentBorderBottomWidth
      )
      contentWrapper.style.height = contentTopToItemMiddle + clampedTriggerMiddleToBottomEdge + "px"
    } else {
      const isFirstItem = items.length > 0 && selectedItem === items[0]
      contentWrapper.style.top = "0px"
      const clampedTopEdgeToTriggerMiddle = Math.max(
        topEdgeToTriggerMiddle,
        contentBorderTopWidth + viewport.offsetTop + (isFirstItem ? viewportPaddingTop : 0) + selectedItemHalfHeight
      )
      contentWrapper.style.height = clampedTopEdgeToTriggerMiddle + itemMiddleToContentBottom + "px"
      viewport.scrollTop = contentTopToItemMiddle - topEdgeToTriggerMiddle + viewport.offsetTop
    }

    contentWrapper.style.margin = `${CONTENT_MARGIN}px 0`
    contentWrapper.style.minHeight = minContentHeight + "px"
    contentWrapper.style.maxHeight = availableHeight + "px"

    onPlaced?.()

    // The scroll position just set must not count as the user scrolling, which would grow the list
    requestAnimationFrame(() => (shouldExpandOnScrollRef.current = true))
  }, [context.trigger, context.valueNode, context.dir, contentWrapper, content, viewport, selectedItem, selectedItemText, onPlaced])

  useLayoutEffect(() => position(), [position])

  // The wrapper makes the stacking context, so it carries the z-index the list asks for
  const [contentZIndex, setContentZIndex] = useState<string>()
  useLayoutEffect(() => {
    if (content) setContentZIndex(window.getComputedStyle(content).zIndex)
  }, [content])

  // A scroll button that appears in the normal flow pushes the viewport, which throws the alignment off by its height: place the list
  // once more when the first one mounts
  const handleScrollButtonChange = useCallback(
    (node: HTMLElement | null) => {
      if (node && shouldRepositionRef.current === true) {
        position()
        focusSelectedItem?.()
        shouldRepositionRef.current = false
      }
    },
    [position, focusSelectedItem]
  )

  return (
    <ViewportContext.Provider value={{ contentWrapper, shouldExpandOnScrollRef, onScrollButtonChange: handleScrollButtonChange }}>
      <div ref={setContentWrapper} style={{ display: "flex", flexDirection: "column", position: "fixed", zIndex: contentZIndex }}>
        {/* The height read from the content includes its borders, so the box has to be border-box; and it must not outgrow the wrapper */}
        <Primitive.div {...popperProps} ref={composedRefs} style={mergeStyles({ boxSizing: "border-box", maxHeight: "100%" }, popperProps.style)} />
      </div>
    </ViewportContext.Provider>
  )
})

const SelectPopperPosition = forwardRef<PositionProps>(({ align = "start", collisionPadding = CONTENT_MARGIN, ...popperProps }, forwardedRef) => (
  <PopperPrimitive.Content
    {...popperProps}
    ref={forwardedRef}
    align={align}
    collisionPadding={collisionPadding}
    style={mergeStyles(
      // Floating UI measures the border box
      { boxSizing: "border-box" },
      popperProps.style,
      {
        "--radix-select-content-transform-origin": "var(--radix-popper-transform-origin)",
        "--radix-select-content-available-width": "var(--radix-popper-available-width)",
        "--radix-select-content-available-height": "var(--radix-popper-available-height)",
        "--radix-select-trigger-width": "var(--radix-popper-anchor-width)",
        "--radix-select-trigger-height": "var(--radix-popper-anchor-height)",
      }
    )}
  />
))

// shortcut: no `nonce` for the scrollbar-hiding style element; add it when a CSP requires one
const SelectViewport = forwardRef<PrimitiveProps<"div">>((viewportProps, forwardedRef) => {
  const contentContext = useSelectContentContext("SelectViewport")
  const viewportContext = useContext(ViewportContext)
  const composedRefs = useComposedRefs(forwardedRef, contentContext.onViewportChange)
  const prevScrollTopRef = useRef(0)
  return (
    <>
      {/* Hides scrollbars across browsers and enables momentum scrolling on touch devices */}
      <style
        dangerouslySetInnerHTML={{
          __html: `[data-radix-select-viewport]{scrollbar-width:none;-ms-overflow-style:none;-webkit-overflow-scrolling:touch;}[data-radix-select-viewport]::-webkit-scrollbar{display:none}`,
        }}
      />
      <Collection.Slot>
        <Primitive.div
          data-radix-select-viewport=""
          role="presentation"
          {...viewportProps}
          ref={composedRefs}
          style={mergeStyles(
            {
              // Positioned so the offsets read from the selected item are relative to the viewport, whatever the scroll buttons add
              position: "relative",
              flex: "1",
              // Only vertical scrolling: this does not hold in vertical writing modes
              overflow: "hidden auto",
            },
            viewportProps.style
          )}
          onScroll={composeEventHandlers(viewportProps.onScroll, (event) => {
            const viewport = event.currentTarget
            const { contentWrapper, shouldExpandOnScrollRef } = viewportContext
            if (shouldExpandOnScrollRef?.current && contentWrapper) {
              const scrolledBy = Math.abs(prevScrollTopRef.current - viewport.scrollTop)
              if (scrolledBy > 0) {
                const availableHeight = window.innerHeight - CONTENT_MARGIN * 2
                const prevHeight = Math.max(parseFloat(contentWrapper.style.minHeight), parseFloat(contentWrapper.style.height))
                // Scrolling grows the list until it fills the viewport
                if (prevHeight < availableHeight) {
                  const nextHeight = prevHeight + scrolledBy
                  const clampedNextHeight = Math.min(availableHeight, nextHeight)
                  const heightDiff = nextHeight - clampedNextHeight
                  contentWrapper.style.height = clampedNextHeight + "px"
                  if (contentWrapper.style.bottom === "0px") {
                    viewport.scrollTop = heightDiff > 0 ? heightDiff : 0
                    // Keeps the list pinned to the bottom
                    contentWrapper.style.justifyContent = "flex-end"
                  }
                }
              }
            }
            prevScrollTopRef.current = viewport.scrollTop
          })}
        />
      </Collection.Slot>
    </>
  )
})

const SelectGroup = forwardRef<PrimitiveProps<"div">>((groupProps, forwardedRef) => {
  const groupId = useId()
  return (
    <SelectGroupProvider value={{ id: groupId }}>
      <Primitive.div role="group" aria-labelledby={groupId} {...groupProps} ref={forwardedRef} />
    </SelectGroupProvider>
  )
})

const SelectLabel = forwardRef<PrimitiveProps<"div">>((labelProps, forwardedRef) => {
  const groupContext = useSelectGroupContext("SelectLabel")
  return <Primitive.div id={groupContext.id} {...labelProps} ref={forwardedRef} />
})

const SelectItem = forwardRef<PrimitiveWith<"div", { value: string; disabled?: boolean; textValue?: string }>>(
  ({ value, disabled = false, textValue: textValueProp, ...itemProps }, forwardedRef) => {
    const context = useSelectContext("SelectItem")
    const contentContext = useSelectContentContext("SelectItem")
    const isSelected = context.value === value
    const [textValue, setTextValue] = useState(textValueProp ?? "")
    const [isFocused, setIsFocused] = useState(false)
    const { itemRefCallback } = contentContext
    const handleItemRef = useCallback((node: HTMLElement | null) => itemRefCallback?.(node, value, disabled), [itemRefCallback, value, disabled])
    const composedRefs = useComposedRefs(forwardedRef, handleItemRef)
    const textId = useId()
    const pointerTypeRef = useRef("touch")

    const handleSelect = () => {
      context.onValueChange(value)
      context.onOpenChange(false)
    }

    return (
      <SelectItemProvider
        value={{
          value,
          disabled,
          textId,
          isSelected,
          onItemTextChange: useCallback((node: HTMLElement | null) => {
            setTextValue((prevTextValue) => prevTextValue || (node?.textContent ?? "").trim())
          }, []),
        }}
      >
        <Collection.ItemSlot value={value} disabled={disabled} textValue={textValue}>
          <Primitive.div
            role="option"
            aria-labelledby={textId}
            data-highlighted={isFocused ? "" : undefined}
            // Selected only once focused: announcing it before that makes VoiceOver stutter
            aria-selected={isSelected && isFocused}
            data-state={isSelected ? "checked" : "unchecked"}
            aria-disabled={disabled || undefined}
            data-disabled={disabled ? "" : undefined}
            tabIndex={disabled ? undefined : -1}
            {...itemProps}
            ref={composedRefs}
            onFocus={composeEventHandlers(itemProps.onFocus, () => setIsFocused(true))}
            onBlur={composeEventHandlers(itemProps.onBlur, () => setIsFocused(false))}
            onClick={composeEventHandlers(itemProps.onClick, () => {
              // A mouse selects on release; touch and pen on click
              if (!disabled && pointerTypeRef.current !== "mouse") handleSelect()
            })}
            onPointerUp={composeEventHandlers(itemProps.onPointerUp, () => {
              // So a press on the trigger can be dragged over the list and released on an item
              if (!disabled && pointerTypeRef.current === "mouse") handleSelect()
            })}
            onPointerDown={composeEventHandlers(itemProps.onPointerDown, (event) => {
              pointerTypeRef.current = event.pointerType
            })}
            onPointerMove={composeEventHandlers(itemProps.onPointerMove, (event) => {
              // The type is remembered for a pointer that slid over from another item
              pointerTypeRef.current = event.pointerType
              if (disabled) contentContext.onItemLeave?.()
              else if (pointerTypeRef.current === "mouse") event.currentTarget.focus({ preventScroll: true })
            })}
            onPointerLeave={composeEventHandlers(itemProps.onPointerLeave, (event) => {
              if (event.currentTarget === document.activeElement) contentContext.onItemLeave?.()
            })}
            onKeyDown={composeEventHandlers(itemProps.onKeyDown, (event) => {
              if (disabled || event.target !== event.currentTarget) return
              const isTypingAhead = contentContext.searchRef?.current !== ""
              if (isTypingAhead && event.key === " ") return
              if (SELECTION_KEYS.includes(event.key)) handleSelect()
              // Keeps Space from scrolling the page
              if (event.key === " ") event.preventDefault()
            })}
          />
        </Collection.ItemSlot>
      </SelectItemProvider>
    )
  }
)

// className and style are dropped: the item is what gets styled
const SelectItemText = forwardRef<PrimitiveProps<"span">>(({ className, style, ...itemTextProps }, forwardedRef) => {
  const contentContext = useSelectContentContext("SelectItemText")
  const itemContext = useSelectItemContext("SelectItemText")
  const valueContent = useValueContent("SelectItemText")
  const { itemTextRefCallback } = contentContext
  const { value, disabled, isSelected, onItemTextChange } = itemContext
  const handleItemTextRef = useCallback((node: HTMLElement | null) => itemTextRefCallback?.(node, value, disabled), [itemTextRefCallback, value, disabled])
  const composedRefs = useComposedRefs(forwardedRef, onItemTextChange, handleItemTextRef)
  const { onContentChange } = valueContent

  // The trigger's value shows what the selected item shows (Radix portals the children there)
  useLayoutEffect(() => {
    if (isSelected) onContentChange({ value, children: itemTextProps.children })
  }, [isSelected, value, itemTextProps.children, onContentChange])

  return <Primitive.span id={itemContext.textId} {...itemTextProps} ref={composedRefs} />
})

const SelectItemIndicator = forwardRef<PrimitiveProps<"span">>((itemIndicatorProps, forwardedRef) => {
  const itemContext = useSelectItemContext("SelectItemIndicator")
  return itemContext.isSelected ? <Primitive.span aria-hidden="true" {...itemIndicatorProps} ref={forwardedRef} /> : null
})

const SelectScrollUpButton = forwardRef<PrimitiveProps<"div">>((props, forwardedRef) => {
  const contentContext = useSelectContentContext("SelectScrollUpButton")
  const viewportContext = useContext(ViewportContext)
  const [canScrollUp, setCanScrollUp] = useState(false)
  const composedRefs = useComposedRefs(forwardedRef, viewportContext.onScrollButtonChange)

  useLayoutEffect(() => {
    if (contentContext.viewport && contentContext.isPositioned) {
      const viewport = contentContext.viewport
      const handleScroll = () => setCanScrollUp(viewport.scrollTop > 0)
      handleScroll()
      viewport.addEventListener("scroll", handleScroll)
      return () => viewport.removeEventListener("scroll", handleScroll)
    }
  }, [contentContext.viewport, contentContext.isPositioned])

  return canScrollUp ? (
    <SelectScrollButtonImpl
      {...props}
      ref={composedRefs}
      onAutoScroll={() => {
        const { viewport, selectedItem } = contentContext
        if (viewport && selectedItem) viewport.scrollTop = viewport.scrollTop - selectedItem.offsetHeight
      }}
    />
  ) : null
})

const SelectScrollDownButton = forwardRef<PrimitiveProps<"div">>((props, forwardedRef) => {
  const contentContext = useSelectContentContext("SelectScrollDownButton")
  const viewportContext = useContext(ViewportContext)
  const [canScrollDown, setCanScrollDown] = useState(false)
  const composedRefs = useComposedRefs(forwardedRef, viewportContext.onScrollButtonChange)

  useLayoutEffect(() => {
    if (contentContext.viewport && contentContext.isPositioned) {
      const viewport = contentContext.viewport
      const handleScroll = () => setCanScrollDown(Math.ceil(viewport.scrollTop) < viewport.scrollHeight - viewport.clientHeight)
      handleScroll()
      viewport.addEventListener("scroll", handleScroll)
      return () => viewport.removeEventListener("scroll", handleScroll)
    }
  }, [contentContext.viewport, contentContext.isPositioned])

  return canScrollDown ? (
    <SelectScrollButtonImpl
      {...props}
      ref={composedRefs}
      onAutoScroll={() => {
        const { viewport, selectedItem } = contentContext
        if (viewport && selectedItem) viewport.scrollTop = viewport.scrollTop + selectedItem.offsetHeight
      }}
    />
  ) : null
})

const SelectScrollButtonImpl = forwardRef<PrimitiveWith<"div", { onAutoScroll(): void }>>(({ onAutoScroll, ...scrollIndicatorProps }, forwardedRef) => {
  const contentContext = useSelectContentContext("SelectScrollButton")
  const autoScrollTimerRef = useRef<number | null>(null)
  const getItems = Collection.useCollection()

  const clearAutoScrollTimer = useCallback(() => {
    if (autoScrollTimerRef.current !== null) {
      window.clearInterval(autoScrollTimerRef.current)
      autoScrollTimerRef.current = null
    }
  }, [])
  const startAutoScroll = () => {
    if (autoScrollTimerRef.current === null) autoScrollTimerRef.current = window.setInterval(onAutoScroll, 50)
  }

  useEffect(() => clearAutoScrollTimer, [clearAutoScrollTimer])

  // The button pushes or shrinks the viewport, which can leave the focused item partly out of view: bring it back
  useLayoutEffect(() => {
    getItems().find((item) => item.ref.current === document.activeElement)?.ref.current?.scrollIntoView({ block: "nearest" })
  }, [getItems])

  return (
    <Primitive.div
      aria-hidden="true"
      {...scrollIndicatorProps}
      ref={forwardedRef}
      style={mergeStyles({ flexShrink: "0" }, scrollIndicatorProps.style)}
      onPointerDown={composeEventHandlers(scrollIndicatorProps.onPointerDown, startAutoScroll)}
      onPointerMove={composeEventHandlers(scrollIndicatorProps.onPointerMove, () => {
        contentContext.onItemLeave?.()
        startAutoScroll()
      })}
      onPointerLeave={composeEventHandlers(scrollIndicatorProps.onPointerLeave, clearAutoScrollTimer)}
    />
  )
})

const SelectSeparator = forwardRef<PrimitiveProps<"div">>((separatorProps, forwardedRef) => (
  <Primitive.div aria-hidden="true" {...separatorProps} ref={forwardedRef} />
))

export {
  Select as Root,
  SelectTrigger as Trigger,
  SelectValue as Value,
  SelectIcon as Icon,
  SelectPortal as Portal,
  SelectContent as Content,
  SelectViewport as Viewport,
  SelectGroup as Group,
  SelectLabel as Label,
  SelectItem as Item,
  SelectItemText as ItemText,
  SelectItemIndicator as ItemIndicator,
  SelectScrollUpButton as ScrollUpButton,
  SelectScrollDownButton as ScrollDownButton,
  SelectSeparator as Separator,
}
export type { SelectContentProps }
