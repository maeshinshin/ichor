import type { ComponentChildren, CSSProperties, RefObject } from "preact"
import { useEffect, useId, useMemo, useRef, useState } from "preact/hooks"
import { useDirection, type Direction } from "../direction"
import { composeEventHandlers } from "./compose-event-handlers"
import { useComposedRefs } from "./compose-refs"
import { createStrictContext } from "./create-context"
import { useDismissableLayer, type DismissableLayerProps } from "./dismissable-layer"
import { useFocusScope } from "./focus-scope"
import { forwardRef } from "./forward-ref"
import { useModalLayer } from "./modal-layer"
import * as PopperPrimitive from "./popper"
import * as PortalPrimitive from "./portal"
import { Presence } from "./presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./primitive"
import { mergeStyles } from "./style"
import { getNextMatch, useTypeahead, wrapArray } from "./typeahead"
import { useControllableState } from "./use-controllable-state"

type Side = NonNullable<PopperPrimitive.PopperContentProps["side"]>
type CheckedState = boolean | "indeterminate"
type Point = { x: number; y: number }
// The area between the pointer and an open submenu, in which hovering other items must not steal focus from it
type GraceIntent = { area: Point[]; side: Side }

const SELECTION_KEYS = ["Enter", " "]
const FIRST_KEYS = ["ArrowDown", "PageUp", "Home"]
const LAST_KEYS = ["ArrowUp", "PageDown", "End"]
const FIRST_LAST_KEYS = [...FIRST_KEYS, ...LAST_KEYS]
const SUB_OPEN_KEYS: Record<Direction, string[]> = { ltr: [...SELECTION_KEYS, "ArrowRight"], rtl: [...SELECTION_KEYS, "ArrowLeft"] }
const SUB_CLOSE_KEYS: Record<Direction, string[]> = { ltr: ["ArrowLeft"], rtl: ["ArrowRight"] }
const ITEM_SELECT = "menu.itemSelect"

const getOpenState = (open: boolean) => (open ? "open" : "closed")
const isIndeterminate = (checked: CheckedState): checked is "indeterminate" => checked === "indeterminate"
const getCheckedState = (checked: CheckedState) => (isIndeterminate(checked) ? "indeterminate" : checked ? "checked" : "unchecked")
const whenMouse =
  <E extends PointerEvent>(handler: (event: E) => void) =>
  (event: E) =>
    event.pointerType === "mouse" ? handler(event) : undefined

// Re-exposes the popper variables under the namespace of the family the menu belongs to
const popperVars = (name: string) =>
  ({
    [`--radix-${name}-content-transform-origin`]: "var(--radix-popper-transform-origin)",
    [`--radix-${name}-content-available-width`]: "var(--radix-popper-available-width)",
    [`--radix-${name}-content-available-height`]: "var(--radix-popper-available-height)",
    [`--radix-${name}-trigger-width`]: "var(--radix-popper-anchor-width)",
    [`--radix-${name}-trigger-height`]: "var(--radix-popper-anchor-height)",
  }) as CSSProperties

function focusFirst(candidates: HTMLElement[], preventScroll = false) {
  const previouslyFocused = document.activeElement
  for (const candidate of candidates) {
    if (candidate === previouslyFocused) return
    candidate.focus({ preventScroll })
    if (document.activeElement !== previouslyFocused) return
  }
}

function isPointInPolygon({ x, y }: Point, polygon: Point[]) {
  let inside = false
  for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
    const a = polygon[i]!
    const b = polygon[j]!
    if (a.y > y !== b.y > y && x < ((b.x - a.x) * (y - a.y)) / (b.y - a.y) + a.x) inside = !inside
  }
  return inside
}

const [MenuProvider, useMenuContext] = createStrictContext<{
  open: boolean
  onOpenChange(open: boolean): void
  content: HTMLElement | null
  onContentChange(content: HTMLElement | null): void
}>("Menu")

const [MenuRootProvider, useMenuRootContext] = createStrictContext<{
  name: string
  onClose(): void
  dir: Direction
  modal: boolean
  isUsingKeyboardRef: RefObject<boolean>
}>("Menu")

const [MenuContentProvider, useMenuContentContext] = createStrictContext<{
  searchRef: RefObject<string>
  onItemEnter(event: PointerEvent): void
  onItemLeave(event: PointerEvent): void
  onTriggerLeave(event: PointerEvent): void
  onPointerGraceIntentChange(intent: GraceIntent | null): void
}>("MenuContent")

const [MenuSubProvider, useMenuSubContext] = createStrictContext<{
  contentId: string
  triggerId: string
  trigger: HTMLElement | null
  onTriggerChange(trigger: HTMLElement | null): void
}>("MenuSub")

const [ItemIndicatorProvider, useItemIndicatorContext] = createStrictContext<CheckedState>("MenuItem")
const [RadioGroupProvider, useRadioGroupContext] = createStrictContext<{ value?: string; onValueChange?(value: string): void }>("MenuRadioGroup")

// `name` is the family (dropdown-menu, context-menu, menubar) whose CSS variable names the content exposes
function Menu({
  name,
  open = false,
  onOpenChange,
  dir,
  modal = true,
  children,
}: {
  name: string
  open?: boolean
  onOpenChange?(open: boolean): void
  dir?: Direction
  modal?: boolean
  children?: ComponentChildren
}) {
  const [content, setContent] = useState<HTMLElement | null>(null)
  const isUsingKeyboardRef = useRef(false)
  const direction = useDirection(dir)

  useEffect(() => {
    // Capture phase, so the flag is set before any handler reacts to the key or pointer that changed it
    const handleKeyDown = () => {
      isUsingKeyboardRef.current = true
      document.addEventListener("pointerdown", handlePointer, { capture: true, once: true })
      document.addEventListener("pointermove", handlePointer, { capture: true, once: true })
    }
    const handlePointer = () => {
      isUsingKeyboardRef.current = false
    }
    document.addEventListener("keydown", handleKeyDown, { capture: true })
    return () => {
      document.removeEventListener("keydown", handleKeyDown, { capture: true })
      document.removeEventListener("pointerdown", handlePointer, { capture: true })
      document.removeEventListener("pointermove", handlePointer, { capture: true })
    }
  }, [])

  useEffect(() => {
    if (!open) return
    const handleBlur = () => onOpenChange?.(false)
    window.addEventListener("blur", handleBlur)
    return () => window.removeEventListener("blur", handleBlur)
  }, [open])

  return (
    <PopperPrimitive.Root>
      <MenuProvider value={{ open, onOpenChange: (open) => onOpenChange?.(open), content, onContentChange: setContent }}>
        <MenuRootProvider value={{ name, onClose: () => onOpenChange?.(false), dir: direction, modal, isUsingKeyboardRef }}>
          {children}
        </MenuRootProvider>
      </MenuProvider>
    </PopperPrimitive.Root>
  )
}

const MenuAnchor = PopperPrimitive.Anchor

function MenuPortal({ children }: { children?: ComponentChildren }) {
  return <PortalPrimitive.Root>{children}</PortalPrimitive.Root>
}

// shortcut: no `forceMount`, `avoidCollisions`, `collisionBoundary`, `collisionPadding`, `sticky`, `hideWhenDetached`, Portal `container`
// or item `textValue` (typeahead reads the item's text); add them if a menu must stay mounted, be tuned against collisions or hold items
// whose text is not what people type
type MenuContentProps = PopperPrimitive.PopperContentProps &
  DismissableLayerProps & {
    loop?: boolean
    onOpenAutoFocus?(event: Event): void
    onCloseAutoFocus?(event: Event): void
    onEntryFocus?(event: Event): void
  }

type MenuContentImplProps = MenuContentProps & { modal: boolean; onDismiss?(): void }

const MenuContentImpl = forwardRef<MenuContentImplProps>(
  (
    { loop = false, modal, onOpenAutoFocus, onCloseAutoFocus, onEntryFocus, onEscapeKeyDown, onPointerDownOutside, onFocusOutside, onInteractOutside, onDismiss, style, ...contentProps },
    forwardedRef
  ) => {
    const context = useMenuContext("MenuContent")
    const rootContext = useMenuRootContext("MenuContent")
    const [node, setNode] = useState<HTMLElement | null>(null)
    const ref = useComposedRefs(forwardedRef, setNode, context.onContentChange)
    const pointerGraceIntentRef = useRef<GraceIntent | null>(null)
    const pointerDirRef = useRef<Side>("right")
    const lastPointerXRef = useRef(0)

    // Read from the page rather than a registry: items register in a passive effect, which runs after the menu has taken
    // focus, and a menu opened with the keyboard has to find its first item right then
    const getItems = () =>
      node
        ? Array.from(node.querySelectorAll<HTMLElement>("[data-radix-collection-item]")).filter(
            (item) => item.closest("[data-radix-menu-content]") === node && !item.hasAttribute("data-disabled")
          )
        : []

    const typeahead = useTypeahead((search) => {
      const items = getItems()
      const textOf = (item: HTMLElement) => (item.textContent ?? "").trim()
      const current = items.find((item) => item === document.activeElement)
      const nextMatch = getNextMatch(items.map(textOf), search, current && textOf(current))
      items.find((item) => textOf(item) === nextMatch)?.focus()
    })

    const isPointerMovingToSubmenu = (event: PointerEvent) => {
      const intent = pointerGraceIntentRef.current
      return !!intent && pointerDirRef.current === intent.side && isPointInPolygon({ x: event.clientX, y: event.clientY }, intent.area)
    }

    // Registered before the scope moves focus, so a layer below does not take that focus for one leaving it
    useDismissableLayer(node, { onEscapeKeyDown, onPointerDownOutside, onFocusOutside, onInteractOutside, onDismiss })
    useFocusScope(node, {
      onMountAutoFocus: composeEventHandlers(onOpenAutoFocus, (event) => {
        // The menu itself takes focus; a first item only follows when the keyboard opened it (see onFocus below)
        event.preventDefault()
        node?.focus({ preventScroll: true })
      }),
      onUnmountAutoFocus: onCloseAutoFocus,
    })
    // A menu that is animating out no longer holds on to the page
    useModalLayer(modal && context.open ? node : null)

    const contentContext = useMemo(
      () => ({
        searchRef: typeahead.search,
        onItemEnter(event: PointerEvent) {
          if (isPointerMovingToSubmenu(event)) event.preventDefault()
        },
        onItemLeave(event: PointerEvent) {
          if (!isPointerMovingToSubmenu(event)) node?.focus()
        },
        onTriggerLeave(event: PointerEvent) {
          if (isPointerMovingToSubmenu(event)) event.preventDefault()
        },
        onPointerGraceIntentChange(intent: GraceIntent | null) {
          pointerGraceIntentRef.current = intent
        },
      }),
      [node]
    )

    return (
      <MenuContentProvider value={contentContext}>
        <PopperPrimitive.Content
          role="menu"
          aria-orientation="vertical"
          data-state={getOpenState(context.open)}
          data-radix-menu-content=""
          data-orientation="vertical"
          dir={rootContext.dir}
          tabIndex={-1}
          {...contentProps}
          ref={ref}
          style={mergeStyles({ outline: "none" }, style, popperVars(rootContext.name))}
          onFocus={composeEventHandlers(contentProps.onFocus, () => {
            const entryFocus = new Event("menu.entryFocus", { cancelable: true })
            onEntryFocus?.(entryFocus)
            if (!rootContext.isUsingKeyboardRef.current) entryFocus.preventDefault()
            if (!entryFocus.defaultPrevented) focusFirst(getItems(), true)
          })}
          onKeyDown={composeEventHandlers(contentProps.onKeyDown, (event) => {
            const target = event.target as HTMLElement
            // Keys from a submenu that is not portaled bubble through here: only the keys of this menu count
            if (target.closest("[data-radix-menu-content]") !== event.currentTarget) return
            // Menus are not navigated with Tab
            if (event.key === "Tab") event.preventDefault()
            if (!event.ctrlKey && !event.altKey && !event.metaKey && event.key.length === 1) typeahead.type(event.key)

            const items = getItems()
            const hasModifier = event.ctrlKey || event.altKey || event.metaKey || event.shiftKey
            if (!FIRST_LAST_KEYS.includes(event.key) || (target !== node && (hasModifier || !items.includes(target)))) return
            event.preventDefault()
            const ordered = LAST_KEYS.includes(event.key) ? [...items].reverse() : items
            // Arrows step from the focused item; Home, End and the menu itself start at an end
            const start = target !== node && event.key.startsWith("Arrow") ? ordered.indexOf(target) + 1 : 0
            focusFirst(loop ? wrapArray(ordered, start) : ordered.slice(start))
          })}
          onPointerMove={composeEventHandlers(
            contentProps.onPointerMove,
            whenMouse((event) => {
              // `movementX` is always 0 in Safari, so compare positions
              if (event.currentTarget.contains(event.target as Node) && lastPointerXRef.current !== event.clientX) {
                pointerDirRef.current = event.clientX > lastPointerXRef.current ? "right" : "left"
                lastPointerXRef.current = event.clientX
              }
            })
          )}
        />
      </MenuContentProvider>
    )
  }
)

const MenuRootContent = forwardRef<MenuContentProps>((props, ref) => {
  const context = useMenuContext("MenuContent")
  const { modal } = useMenuRootContext("MenuContent")
  return (
    <MenuContentImpl
      {...props}
      ref={ref}
      modal={modal}
      // The page is out of reach while a modal menu is open, so a focus change cannot mean the user left
      onFocusOutside={modal ? composeEventHandlers(props.onFocusOutside, (event) => event.preventDefault(), { checkForDefaultPrevented: false }) : props.onFocusOutside}
      onDismiss={() => context.onOpenChange(false)}
    />
  )
})

const MenuContent = forwardRef<MenuContentProps>((props, ref) => {
  const context = useMenuContext("MenuContent")
  return (
    <Presence present={context.open}>
      <MenuRootContent {...props} ref={ref} />
    </Presence>
  )
})

const MenuGroup = forwardRef<PrimitiveProps<"div">>((props, ref) => <Primitive.div role="group" {...props} ref={ref} />)

const MenuLabel = forwardRef<PrimitiveProps<"div">>((props, ref) => <Primitive.div {...props} ref={ref} />)

type MenuItemImplProps = PrimitiveWith<"div", { disabled?: boolean }>

const MenuItemImpl = forwardRef<MenuItemImplProps>(({ disabled = false, ...itemProps }, forwardedRef) => {
  const contentContext = useMenuContentContext("MenuItem")
  const [isFocused, setIsFocused] = useState(false)
  return (
    <Primitive.div
      role="menuitem"
      tabIndex={-1}
      data-radix-collection-item=""
      data-orientation="vertical"
      data-highlighted={isFocused ? "" : undefined}
      aria-disabled={disabled || undefined}
      data-disabled={disabled ? "" : undefined}
      {...itemProps}
      ref={forwardedRef}
      onPointerMove={composeEventHandlers(
        itemProps.onPointerMove,
        whenMouse((event) => {
          if (disabled) contentContext.onItemLeave(event)
          else {
            contentContext.onItemEnter(event)
            if (!event.defaultPrevented) event.currentTarget.focus({ preventScroll: true })
          }
        })
      )}
      onPointerLeave={composeEventHandlers(itemProps.onPointerLeave, whenMouse(contentContext.onItemLeave))}
      onFocus={composeEventHandlers(itemProps.onFocus, () => setIsFocused(true))}
      onBlur={composeEventHandlers(itemProps.onBlur, () => setIsFocused(false))}
    />
  )
})

type MenuItemProps = MenuItemImplProps & { onSelect?(event: Event): void }

const MenuItem = forwardRef<MenuItemProps>(({ disabled = false, onSelect, ...itemProps }, forwardedRef) => {
  const ref = useRef<HTMLDivElement | null>(null)
  const rootContext = useMenuRootContext("MenuItem")
  const contentContext = useMenuContentContext("MenuItem")
  const composedRefs = useComposedRefs(forwardedRef, ref)
  const isPointerDownRef = useRef(false)

  const handleSelect = () => {
    const menuItem = ref.current
    if (disabled || !menuItem) return
    const itemSelectEvent = new CustomEvent(ITEM_SELECT, { bubbles: true, cancelable: true })
    if (onSelect) menuItem.addEventListener(ITEM_SELECT, onSelect, { once: true })
    menuItem.dispatchEvent(itemSelectEvent)
    if (itemSelectEvent.defaultPrevented) isPointerDownRef.current = false
    else rootContext.onClose()
  }

  return (
    <MenuItemImpl
      {...itemProps}
      ref={composedRefs}
      disabled={disabled}
      onClick={composeEventHandlers(itemProps.onClick, handleSelect)}
      onPointerDown={(event) => {
        itemProps.onPointerDown?.(event)
        isPointerDownRef.current = true
      }}
      onPointerUp={composeEventHandlers(itemProps.onPointerUp, (event) => {
        // A press that started on another item selects this one on release. Going through a click lets click based
        // triggers compose with it and keeps Firefox from getting stuck in text selection when the menu closes.
        if (!isPointerDownRef.current) event.currentTarget.click()
      })}
      onKeyDown={composeEventHandlers(itemProps.onKeyDown, (event) => {
        if (disabled || event.target !== event.currentTarget) return
        const isTypingAhead = contentContext.searchRef.current !== ""
        if (isTypingAhead && event.key === " ") return
        if (SELECTION_KEYS.includes(event.key)) {
          event.currentTarget.click()
          // Keeps Space from scrolling the page
          event.preventDefault()
        }
      })}
    />
  )
})

type MenuCheckboxItemProps = MenuItemProps & { checked?: CheckedState; onCheckedChange?(checked: boolean): void }

const MenuCheckboxItem = forwardRef<MenuCheckboxItemProps>(({ checked = false, onCheckedChange, ...checkboxItemProps }, forwardedRef) => (
  <ItemIndicatorProvider value={checked}>
    <MenuItem
      role="menuitemcheckbox"
      aria-checked={isIndeterminate(checked) ? "mixed" : checked}
      data-state={getCheckedState(checked)}
      {...checkboxItemProps}
      ref={forwardedRef}
      onSelect={composeEventHandlers(checkboxItemProps.onSelect, () => onCheckedChange?.(isIndeterminate(checked) ? true : !checked), { checkForDefaultPrevented: false })}
    />
  </ItemIndicatorProvider>
))

type MenuRadioGroupProps = PrimitiveWith<"div", { value?: string; onValueChange?(value: string): void }>

const MenuRadioGroup = forwardRef<MenuRadioGroupProps>(({ value, onValueChange, ...groupProps }, ref) => (
  <RadioGroupProvider value={{ value, onValueChange }}>
    <MenuGroup {...groupProps} ref={ref} />
  </RadioGroupProvider>
))

const MenuRadioItem = forwardRef<MenuItemProps & { value: string }>(({ value, ...radioItemProps }, forwardedRef) => {
  const context = useRadioGroupContext("MenuRadioItem")
  const checked = value === context.value
  return (
    <ItemIndicatorProvider value={checked}>
      <MenuItem
        role="menuitemradio"
        aria-checked={checked}
        data-state={getCheckedState(checked)}
        {...radioItemProps}
        ref={forwardedRef}
        onSelect={composeEventHandlers(radioItemProps.onSelect, () => context.onValueChange?.(value), { checkForDefaultPrevented: false })}
      />
    </ItemIndicatorProvider>
  )
})

const MenuItemIndicator = forwardRef<PrimitiveProps<"span">>((props, ref) => {
  const checked = useItemIndicatorContext("MenuItemIndicator")
  return (
    <Presence present={isIndeterminate(checked) || checked === true}>
      <Primitive.span {...props} ref={ref} data-state={getCheckedState(checked)} />
    </Presence>
  )
})

const MenuSeparator = forwardRef<PrimitiveProps<"div">>((props, ref) => (
  <Primitive.div role="separator" aria-orientation="horizontal" {...props} ref={ref} />
))

function MenuSub({
  open: openProp,
  defaultOpen,
  onOpenChange,
  children,
}: {
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?(open: boolean): void
  children?: ComponentChildren
}) {
  const parentMenuContext = useMenuContext("MenuSub")
  const [trigger, setTrigger] = useState<HTMLElement | null>(null)
  const [content, setContent] = useState<HTMLElement | null>(null)
  const [open, setOpen] = useControllableState({ prop: openProp, defaultProp: defaultOpen ?? false, onChange: onOpenChange })

  // Prevent the parent menu from reopening with open submenus
  useEffect(() => {
    if (parentMenuContext.open === false) setOpen(false)
    return () => setOpen(false)
  }, [parentMenuContext.open, setOpen])

  return (
    <PopperPrimitive.Root>
      <MenuProvider value={{ open, onOpenChange: setOpen, content, onContentChange: setContent }}>
        <MenuSubProvider value={{ contentId: useId(), triggerId: useId(), trigger, onTriggerChange: setTrigger }}>{children}</MenuSubProvider>
      </MenuProvider>
    </PopperPrimitive.Root>
  )
}

const MenuSubTrigger = forwardRef<MenuItemImplProps>((props, forwardedRef) => {
  const context = useMenuContext("MenuSubTrigger")
  const rootContext = useMenuRootContext("MenuSubTrigger")
  const subContext = useMenuSubContext("MenuSubTrigger")
  const contentContext = useMenuContentContext("MenuSubTrigger")
  const openTimerRef = useRef(0)
  const pointerGraceTimerRef = useRef(0)
  const ref = useComposedRefs(forwardedRef, subContext.onTriggerChange)
  const { onPointerGraceIntentChange } = contentContext

  const clearOpenTimer = () => {
    clearTimeout(openTimerRef.current)
    openTimerRef.current = 0
  }
  useEffect(() => clearOpenTimer, [])
  useEffect(
    () => () => {
      clearTimeout(pointerGraceTimerRef.current)
      onPointerGraceIntentChange(null)
    },
    [onPointerGraceIntentChange]
  )

  return (
    <PopperPrimitive.Anchor asChild>
      <MenuItemImpl
        id={subContext.triggerId}
        aria-haspopup="menu"
        aria-expanded={context.open}
        aria-controls={context.open ? subContext.contentId : undefined}
        data-state={getOpenState(context.open)}
        {...props}
        ref={ref}
        // Redundant for mouse users, but the pointer type of a click is unknown and pointerup is no reliable stand-in
        onClick={(event) => {
          props.onClick?.(event)
          if (props.disabled || event.defaultPrevented) return
          // iOS Safari does not always focus on click, and submenus rely on focus leaving them to close
          event.currentTarget.focus()
          if (!context.open) context.onOpenChange(true)
        }}
        onPointerMove={composeEventHandlers(
          props.onPointerMove,
          whenMouse((event) => {
            contentContext.onItemEnter(event)
            if (event.defaultPrevented) return
            if (!props.disabled && !context.open && !openTimerRef.current) {
              contentContext.onPointerGraceIntentChange(null)
              openTimerRef.current = window.setTimeout(() => {
                context.onOpenChange(true)
                clearOpenTimer()
              }, 100)
            }
          })
        )}
        onPointerLeave={composeEventHandlers(
          props.onPointerLeave,
          whenMouse((event) => {
            clearOpenTimer()
            const contentRect = context.content?.getBoundingClientRect()
            if (contentRect) {
              const side = context.content!.dataset.side as Side
              const rightSide = side === "right"
              // A bleed on clientX keeps the exit point inside the polygon
              const bleed = rightSide ? -5 : 5
              const contentNearEdge = contentRect[rightSide ? "left" : "right"]
              const contentFarEdge = contentRect[rightSide ? "right" : "left"]
              contentContext.onPointerGraceIntentChange({
                area: [
                  { x: event.clientX + bleed, y: event.clientY },
                  { x: contentNearEdge, y: contentRect.top },
                  { x: contentFarEdge, y: contentRect.top },
                  { x: contentFarEdge, y: contentRect.bottom },
                  { x: contentNearEdge, y: contentRect.bottom },
                ],
                side,
              })
              clearTimeout(pointerGraceTimerRef.current)
              pointerGraceTimerRef.current = window.setTimeout(() => contentContext.onPointerGraceIntentChange(null), 300)
            } else {
              contentContext.onTriggerLeave(event)
              if (event.defaultPrevented) return
              // The pointer may leave the item in the 100ms before the submenu opens
              contentContext.onPointerGraceIntentChange(null)
            }
          })
        )}
        onKeyDown={composeEventHandlers(props.onKeyDown, (event) => {
          if (props.disabled || event.target !== event.currentTarget) return
          const isTypingAhead = contentContext.searchRef.current !== ""
          if (isTypingAhead && event.key === " ") return
          if (SUB_OPEN_KEYS[rootContext.dir].includes(event.key)) {
            context.onOpenChange(true)
            // The trigger may still hold focus from a pointer interaction: hand it to the content
            context.content?.focus()
            event.preventDefault()
          }
        })}
      />
    </PopperPrimitive.Anchor>
  )
})

type MenuSubContentProps = Omit<MenuContentProps, "onCloseAutoFocus" | "onEntryFocus" | "side">

const MenuSubContent = forwardRef<MenuSubContentProps>(({ align = "start", ...props }, forwardedRef) => {
  const context = useMenuContext("MenuSubContent")
  const rootContext = useMenuRootContext("MenuSubContent")
  const subContext = useMenuSubContext("MenuSubContent")
  const ref = useRef<HTMLDivElement | null>(null)
  const composedRefs = useComposedRefs(forwardedRef, ref)
  return (
    <Presence present={context.open}>
      <MenuContentImpl
        id={subContext.contentId}
        aria-labelledby={subContext.triggerId}
        {...props}
        ref={composedRefs}
        align={align}
        side={rootContext.dir === "rtl" ? "left" : "right"}
        modal={false}
        onOpenAutoFocus={(event) => {
          // A submenu opened with the pointer leaves focus on its trigger
          if (rootContext.isUsingKeyboardRef.current) ref.current?.focus()
          event.preventDefault()
        }}
        // The submenu may close because another item of the parent got focus: the trigger gets its focus back by hand instead
        onCloseAutoFocus={(event) => event.preventDefault()}
        onFocusOutside={composeEventHandlers(props.onFocusOutside, (event) => {
          // Not when the trigger is focused, which would replay the opening animation on a pointer interaction
          if (event.target !== subContext.trigger) context.onOpenChange(false)
        })}
        onEscapeKeyDown={composeEventHandlers(props.onEscapeKeyDown, (event) => {
          rootContext.onClose()
          event.preventDefault()
        })}
        onKeyDown={composeEventHandlers(props.onKeyDown, (event) => {
          // Keys of a deeper submenu bubble through here when it is not portaled
          const isKeyDownInside = event.currentTarget.contains(event.target as Node)
          if (isKeyDownInside && SUB_CLOSE_KEYS[rootContext.dir].includes(event.key)) {
            context.onOpenChange(false)
            // Focus was kept off the trigger by onCloseAutoFocus
            subContext.trigger?.focus()
            event.preventDefault()
          }
        })}
      />
    </Presence>
  )
})

export {
  Menu as Root,
  MenuAnchor as Anchor,
  MenuPortal as Portal,
  MenuContent as Content,
  MenuGroup as Group,
  MenuLabel as Label,
  MenuItem as Item,
  MenuCheckboxItem as CheckboxItem,
  MenuRadioGroup as RadioGroup,
  MenuRadioItem as RadioItem,
  MenuItemIndicator as ItemIndicator,
  MenuSeparator as Separator,
  MenuSub as Sub,
  MenuSubTrigger as SubTrigger,
  MenuSubContent as SubContent,
}
export type { MenuContentProps, MenuSubContentProps }
