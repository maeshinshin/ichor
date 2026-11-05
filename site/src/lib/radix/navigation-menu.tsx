import { h, type FunctionComponent, type HTMLAttributes, type Ref, type VNode } from "preact"
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks"
import { useDirection, type Direction } from "./direction"
import { createCollection } from "./internal/collection"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { useDismissableLayer, type DismissableLayerProps } from "./internal/dismissable-layer"
import { getTabbableCandidates } from "./internal/focus-scope"
import { forwardRef } from "./internal/forward-ref"
import { joinClass } from "./internal/merge-props"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { mergeStyles } from "./internal/style"
import { useControllableState } from "./internal/use-controllable-state"
import { useSize } from "./internal/use-size"
import { VisuallyHidden } from "./internal/visually-hidden"
import { Root as Slot } from "./slot"

type Orientation = "horizontal" | "vertical"

// Primitive has no nav, ul or li
type TagProps<E extends HTMLElement> = Omit<HTMLAttributes<E>, "ref"> & { asChild?: boolean; ref?: Ref<any> }
type TagWith<E extends HTMLElement, Own> = Omit<TagProps<E>, keyof Own> & Own

function createTag<E extends HTMLElement>(tag: "nav" | "ul" | "li") {
  return forwardRef<TagProps<E>>(({ asChild, class: classProp, className, ...props }: any, ref) =>
    h(asChild ? Slot : tag, { ...props, class: joinClass(classProp, className), ref })
  ) as FunctionComponent<TagProps<E>>
}
const Nav = createTag<HTMLElement>("nav")
const Ul = createTag<HTMLUListElement>("ul")
const Li = createTag<HTMLLIElement>("li")

// What `useRef<HTMLElement | null>(null)` gives on both Preact 10 and 11 (their RefObject differs)
type ElementRef = { current: HTMLElement | null }

type ContentProps = PrimitiveProps<"div"> & DismissableLayerProps

// What Content hands over to the Viewport, or to its own element when there is no Viewport
type ContentImplProps = ContentProps & {
  value: string
  triggerRef: ElementRef
  focusProxyRef: ElementRef
  wasEscapeCloseRef: { current: boolean }
  onRootContentClose(): void
  onContentFocusOutside(): void
}

// Content and the Indicator are written where the caller puts them but shown elsewhere (in the Viewport, in the list track).
// Preact has no portal outside preact/compat, so they register what to render and the place that shows them subscribes.
function createStore<T>() {
  const entries = new Map<string, T>()
  const listeners = new Set<() => void>()
  const notify = () => listeners.forEach((listener) => listener())
  return {
    entries,
    set(key: string, entry: T) {
      entries.set(key, entry)
      notify()
    },
    delete(key: string) {
      if (entries.delete(key)) notify()
    },
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => void listeners.delete(listener)
    },
  }
}
type Store<T> = ReturnType<typeof createStore<T>>

function useStore<T>(store: Store<T>) {
  const [, setVersion] = useState(0)
  useLayoutEffect(() => {
    const update = () => setVersion((version) => version + 1)
    // Whatever registered before this subscribed
    update()
    return store.subscribe(update)
  }, [store])
  return store.entries
}

const INDICATOR = "indicator"
const LINK_SELECT = "navigationMenu.linkSelect"
const ROOT_CONTENT_DISMISS = "navigationMenu.rootContentDismiss"

const TriggerCollection = createCollection<{ value: string }>()
const FocusCollection = createCollection()

const [NavigationMenuProvider, useNavigationMenuContext] = createStrictContext<{
  baseId: string
  value: string
  previousValue: string
  dir: Direction
  orientation: Orientation
  rootRef: ElementRef
  viewport: HTMLElement | null
  onViewportChange(node: HTMLElement | null): void
  indicatorTrack: HTMLElement | null
  onIndicatorTrackChange(node: HTMLElement | null): void
  triggers: Map<string, ElementRef>
  contents: Store<ContentImplProps>
  indicator: Store<VNode<any>>
  onTriggerEnter(itemValue: string): void
  onTriggerLeave(): void
  onContentEnter(): void
  onContentLeave(): void
  onItemSelect(itemValue: string): void
  onItemDismiss(): void
}>("NavigationMenu")

const [NavigationMenuItemProvider, useNavigationMenuItemContext] = createStrictContext<{
  value: string
  triggerRef: ElementRef
  contentRef: ElementRef
  focusProxyRef: ElementRef
  wasEscapeCloseRef: { current: boolean }
  onEntryKeyDown(): void
  onFocusProxyEnter(side: "start" | "end"): void
  onRootContentClose(): void
  onContentFocusOutside(): void
}>("NavigationMenuItem")

const getOpenState = (open: boolean) => (open ? "open" : "closed")
const makeTriggerId = (baseId: string, value: string) => `${baseId}-trigger-${value}`
const makeContentId = (baseId: string, value: string) => `${baseId}-content-${value}`
const whenMouse = (handler: () => void) => (event: PointerEvent) => (event.pointerType === "mouse" ? handler() : undefined)

function focusFirst(candidates: HTMLElement[]) {
  const previouslyFocused = document.activeElement
  return candidates.some((candidate) => {
    if (candidate === previouslyFocused) return true
    candidate.focus()
    return document.activeElement !== previouslyFocused
  })
}

function removeFromTabOrder(candidates: HTMLElement[]) {
  for (const candidate of candidates) {
    candidate.dataset.tabindex = candidate.getAttribute("tabindex") || ""
    candidate.setAttribute("tabindex", "-1")
  }
  return () => {
    for (const candidate of candidates) candidate.setAttribute("tabindex", candidate.dataset.tabindex!)
  }
}

// shortcut: no NavigationMenu.Sub, `activationMode` or `forceMount`, and no `asChild` on the Viewport; upstream uses none of them,
// add them when a caller needs nested menus, click-only activation or a custom viewport element
const NavigationMenu = forwardRef<
  TagWith<
    HTMLElement,
    {
      value?: string
      defaultValue?: string
      onValueChange?(value: string): void
      delayDuration?: number
      skipDelayDuration?: number
      orientation?: Orientation
      dir?: Direction
    }
  >
>(
  (
    { value: valueProp, onValueChange, defaultValue, delayDuration = 200, skipDelayDuration = 300, orientation = "horizontal", dir, ...rootProps },
    forwardedRef
  ) => {
    const rootRef = useRef<HTMLElement | null>(null)
    const ref = useComposedRefs(forwardedRef, rootRef)
    const direction = useDirection(dir)
    const baseId = useId()
    const openTimerRef = useRef(0)
    const closeTimerRef = useRef(0)
    const skipDelayTimerRef = useRef(0)
    const [isOpenDelayed, setIsOpenDelayed] = useState(true)
    const [viewport, setViewport] = useState<HTMLElement | null>(null)
    const [indicatorTrack, setIndicatorTrack] = useState<HTMLElement | null>(null)
    const [triggers] = useState(() => new Map<string, ElementRef>())
    const [contents] = useState(createStore<ContentImplProps>)
    const [indicator] = useState(createStore<VNode<any>>)
    const [value, setValue] = useControllableState({
      prop: valueProp,
      defaultProp: defaultValue ?? "",
      onChange(nextValue) {
        clearTimeout(skipDelayTimerRef.current)
        if (nextValue) {
          if (skipDelayDuration > 0) setIsOpenDelayed(false)
        } else skipDelayTimerRef.current = window.setTimeout(() => setIsOpenDelayed(true), skipDelayDuration)
        onValueChange?.(nextValue)
      },
    })

    // The value before the latest change, which content uses to pick the direction it moves in
    const lastChange = useRef({ value, previous: value })
    if (lastChange.current.value !== value) lastChange.current = { value, previous: lastChange.current.value }

    const startCloseTimer = () => {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = window.setTimeout(() => setValue(""), 150)
    }
    const handleOpen = (itemValue: string) => {
      clearTimeout(closeTimerRef.current)
      setValue(itemValue)
    }
    const handleDelayedOpen = (itemValue: string) => {
      // Already open (the pointer came back from the content): only keep it from closing
      if (value === itemValue) clearTimeout(closeTimerRef.current)
      else
        openTimerRef.current = window.setTimeout(() => {
          clearTimeout(closeTimerRef.current)
          setValue(itemValue)
        }, delayDuration)
    }
    useEffect(
      () => () => {
        clearTimeout(openTimerRef.current)
        clearTimeout(closeTimerRef.current)
        clearTimeout(skipDelayTimerRef.current)
      },
      []
    )

    return (
      <NavigationMenuProvider
        value={{
          baseId,
          value,
          previousValue: lastChange.current.previous,
          dir: direction,
          orientation,
          rootRef,
          viewport,
          onViewportChange: setViewport,
          indicatorTrack,
          onIndicatorTrackChange: setIndicatorTrack,
          triggers,
          contents,
          indicator,
          onTriggerEnter(itemValue) {
            clearTimeout(openTimerRef.current)
            if (isOpenDelayed) handleDelayedOpen(itemValue)
            else handleOpen(itemValue)
          },
          onTriggerLeave() {
            clearTimeout(openTimerRef.current)
            startCloseTimer()
          },
          onContentEnter: () => clearTimeout(closeTimerRef.current),
          onContentLeave: startCloseTimer,
          onItemSelect: (itemValue) => setValue((previous) => (previous === itemValue ? "" : itemValue)),
          onItemDismiss: () => setValue(""),
        }}
      >
        <TriggerCollection.Provider>
          <Nav aria-label="Main" data-orientation={orientation} dir={direction} {...rootProps} ref={ref} />
        </TriggerCollection.Provider>
      </NavigationMenuProvider>
    )
  }
)

const NavigationMenuList = forwardRef<TagProps<HTMLUListElement>>((props, forwardedRef) => {
  const context = useNavigationMenuContext("NavigationMenuList")
  const indicator = useStore(context.indicator).get(INDICATOR)
  return (
    <Primitive.div style={{ position: "relative" }} ref={context.onIndicatorTrackChange}>
      <TriggerCollection.Slot>
        <FocusGroup asChild>
          <Ul data-orientation={context.orientation} {...props} ref={forwardedRef} />
        </FocusGroup>
      </TriggerCollection.Slot>
      {indicator && <Presence present={Boolean(context.value)}>{indicator}</Presence>}
    </Primitive.div>
  )
})

const NavigationMenuItem = forwardRef<TagWith<HTMLLIElement, { value?: string }>>(({ value: valueProp, ...itemProps }, forwardedRef) => {
  const { triggers } = useNavigationMenuContext("NavigationMenuItem")
  const autoValue = useId()
  const value = valueProp || autoValue
  const contentRef = useRef<HTMLElement | null>(null)
  const triggerRef = useRef<HTMLElement | null>(null)
  const focusProxyRef = useRef<HTMLElement | null>(null)
  const restoreContentTabOrderRef = useRef(() => {})
  const wasEscapeCloseRef = useRef(false)
  // For the indicator, which cannot use the trigger collection (it fills in an effect) or the trigger's id (callers set their own)
  useLayoutEffect(() => {
    triggers.set(value, triggerRef)
    return () => void triggers.delete(value)
  }, [triggers, value])

  const handleContentEntry = useCallback((side: "start" | "end" = "start") => {
    if (!contentRef.current) return
    restoreContentTabOrderRef.current()
    const candidates = getTabbableCandidates(contentRef.current)
    if (candidates.length) focusFirst(side === "start" ? candidates : candidates.reverse())
  }, [])
  const handleContentExit = useCallback(() => {
    if (!contentRef.current) return
    const candidates = getTabbableCandidates(contentRef.current)
    if (candidates.length) restoreContentTabOrderRef.current = removeFromTabOrder(candidates)
  }, [])

  return (
    <NavigationMenuItemProvider
      value={{
        value,
        triggerRef,
        contentRef,
        focusProxyRef,
        wasEscapeCloseRef,
        onEntryKeyDown: handleContentEntry,
        onFocusProxyEnter: handleContentEntry,
        onRootContentClose: handleContentExit,
        onContentFocusOutside: handleContentExit,
      }}
    >
      <Li {...itemProps} ref={forwardedRef} />
    </NavigationMenuItemProvider>
  )
})

const NavigationMenuTrigger = forwardRef<PrimitiveWith<"button", { disabled?: boolean }>>(({ disabled, ...triggerProps }, forwardedRef) => {
  const context = useNavigationMenuContext("NavigationMenuTrigger")
  const itemContext = useNavigationMenuItemContext("NavigationMenuTrigger")
  const ref = useComposedRefs(itemContext.triggerRef, forwardedRef)
  const contentId = makeContentId(context.baseId, itemContext.value)
  const hasPointerMoveOpenedRef = useRef(false)
  const wasClickCloseRef = useRef(false)
  const open = itemContext.value === context.value

  return (
    <>
      <TriggerCollection.ItemSlot value={itemContext.value}>
        <FocusGroupItem asChild>
          <Primitive.button
            id={makeTriggerId(context.baseId, itemContext.value)}
            disabled={disabled}
            data-disabled={disabled ? "" : undefined}
            data-state={getOpenState(open)}
            aria-expanded={open}
            aria-controls={open ? contentId : undefined}
            {...triggerProps}
            ref={ref}
            onPointerEnter={composeEventHandlers(triggerProps.onPointerEnter, () => {
              wasClickCloseRef.current = false
              itemContext.wasEscapeCloseRef.current = false
            })}
            onPointerMove={composeEventHandlers(
              triggerProps.onPointerMove,
              whenMouse(() => {
                if (disabled || wasClickCloseRef.current || itemContext.wasEscapeCloseRef.current || hasPointerMoveOpenedRef.current) return
                context.onTriggerEnter(itemContext.value)
                hasPointerMoveOpenedRef.current = true
              })
            )}
            onPointerLeave={composeEventHandlers(
              triggerProps.onPointerLeave,
              whenMouse(() => {
                if (disabled) return
                context.onTriggerLeave()
                hasPointerMoveOpenedRef.current = false
              })
            )}
            onClick={composeEventHandlers(triggerProps.onClick, () => {
              context.onItemSelect(itemContext.value)
              wasClickCloseRef.current = open
            })}
            onKeyDown={composeEventHandlers(triggerProps.onKeyDown, (event) => {
              const entryKey = context.orientation === "horizontal" ? "ArrowDown" : context.dir === "rtl" ? "ArrowLeft" : "ArrowRight"
              if (open && event.key === entryKey) {
                itemContext.onEntryKeyDown()
                // Keeps the focus group from also moving to the next item
                event.preventDefault()
              }
            })}
          />
        </FocusGroupItem>
      </TriggerCollection.ItemSlot>

      {/* A tab stop between the trigger and its content, which can be elsewhere in the page when there is a Viewport */}
      {open && (
        <>
          <VisuallyHidden
            aria-hidden
            tabIndex={0}
            ref={itemContext.focusProxyRef}
            onFocus={(event) => {
              const previouslyFocused = event.relatedTarget as HTMLElement | null
              const wasTriggerFocused = previouslyFocused === itemContext.triggerRef.current
              const wasFocusFromContent = itemContext.contentRef.current?.contains(previouslyFocused)
              if (wasTriggerFocused || !wasFocusFromContent) itemContext.onFocusProxyEnter(wasTriggerFocused ? "start" : "end")
            }}
          />
          {context.viewport && <span aria-owns={contentId} />}
        </>
      )}
    </>
  )
})

const NavigationMenuLink = forwardRef<PrimitiveWith<"a", { active?: boolean; onSelect?(event: Event): void }>>(
  ({ active, onSelect, ...linkProps }, forwardedRef) => (
    <FocusGroupItem asChild>
      <Primitive.a
        data-active={active ? "" : undefined}
        aria-current={active ? "page" : undefined}
        {...linkProps}
        ref={forwardedRef}
        onClick={composeEventHandlers(
          linkProps.onClick,
          (event) => {
            const target = event.target as HTMLElement
            const linkSelectEvent = new CustomEvent(LINK_SELECT, { bubbles: true, cancelable: true })
            target.addEventListener(LINK_SELECT, (event) => onSelect?.(event), { once: true })
            target.dispatchEvent(linkSelectEvent)
            // The content the link is in listens for this and closes the menu
            if (!linkSelectEvent.defaultPrevented && !event.metaKey)
              target.dispatchEvent(new CustomEvent(ROOT_CONTENT_DISMISS, { bubbles: true, cancelable: true }))
          },
          // A link that navigates itself prevents the default of its click, which still has to close the menu
          { checkForDefaultPrevented: false }
        )}
      />
    </FocusGroupItem>
  )
)

const NavigationMenuIndicator = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const { indicator } = useNavigationMenuContext("NavigationMenuIndicator")
  const rendered = <NavigationMenuIndicatorImpl {...props} ref={ref} />
  // Shown by the List, inside its track; registered after every render so it shows the latest props
  useLayoutEffect(() => indicator.set(INDICATOR, rendered))
  useLayoutEffect(() => () => indicator.delete(INDICATOR), [indicator])
  return null
})

const NavigationMenuIndicatorImpl = forwardRef<PrimitiveProps<"div">>((props, forwardedRef) => {
  const context = useNavigationMenuContext("NavigationMenuIndicator")
  const [activeTrigger, setActiveTrigger] = useState<HTMLElement | null>(null)
  const isHorizontal = context.orientation === "horizontal"

  useEffect(() => {
    const trigger = context.triggers.get(context.value)?.current
    // Closing finds none: the indicator stays under the last trigger while it animates out
    if (trigger) setActiveTrigger(trigger)
  }, [context.triggers, context.value])
  // Positions are read below; these re-render when the trigger or the track changes size
  useSize(activeTrigger)
  useSize(context.indicatorTrack)

  if (!activeTrigger) return null
  const size = isHorizontal ? activeTrigger.offsetWidth : activeTrigger.offsetHeight
  const offset = isHorizontal ? activeTrigger.offsetLeft : activeTrigger.offsetTop
  return (
    <Primitive.div
      aria-hidden
      data-state={context.value ? "visible" : "hidden"}
      data-orientation={context.orientation}
      {...props}
      ref={forwardedRef}
      style={mergeStyles(
        { position: "absolute" },
        isHorizontal
          ? { left: "0px", width: `${size}px`, transform: `translateX(${offset}px)`, "--radix-navigation-menu-indicator-translate-x": `${offset}px` }
          : { top: "0px", height: `${size}px`, transform: `translateY(${offset}px)`, "--radix-navigation-menu-indicator-translate-y": `${offset}px` },
        props.style
      )}
    />
  )
})

const NavigationMenuContent = forwardRef<ContentProps>((props, forwardedRef) => {
  const context = useNavigationMenuContext("NavigationMenuContent")
  const itemContext = useNavigationMenuItemContext("NavigationMenuContent")
  const ref = useComposedRefs(itemContext.contentRef, forwardedRef)
  const open = itemContext.value === context.value
  const commonProps = {
    value: itemContext.value,
    triggerRef: itemContext.triggerRef,
    focusProxyRef: itemContext.focusProxyRef,
    wasEscapeCloseRef: itemContext.wasEscapeCloseRef,
    onContentFocusOutside: itemContext.onContentFocusOutside,
    onRootContentClose: itemContext.onRootContentClose,
    ...props,
  }

  return context.viewport ? (
    <ViewportContentMounter {...commonProps} ref={ref} />
  ) : (
    <Presence present={open}>
      <NavigationMenuContentImpl
        data-state={getOpenState(open)}
        {...commonProps}
        ref={ref}
        onPointerEnter={composeEventHandlers(props.onPointerEnter, context.onContentEnter)}
        onPointerLeave={composeEventHandlers(props.onPointerLeave, whenMouse(context.onContentLeave))}
        // Nothing can be clicked while it animates out
        style={mergeStyles({ pointerEvents: open ? undefined : "none" }, props.style)}
      />
    </Presence>
  )
})

const ViewportContentMounter = forwardRef<ContentImplProps>((props, ref) => {
  const { contents } = useNavigationMenuContext("NavigationMenuContent")
  // Registered after every render so the Viewport shows the latest props
  useLayoutEffect(() => contents.set(props.value, { ...props, ref }))
  useLayoutEffect(() => () => contents.delete(props.value), [contents, props.value])
  return null
})

const NavigationMenuContentImpl = forwardRef<ContentImplProps>(
  (
    {
      value,
      triggerRef,
      focusProxyRef,
      wasEscapeCloseRef,
      onRootContentClose,
      onContentFocusOutside,
      onEscapeKeyDown,
      onPointerDownOutside,
      onFocusOutside,
      onInteractOutside,
      ...contentProps
    },
    forwardedRef
  ) => {
    const context = useNavigationMenuContext("NavigationMenuContent")
    const [node, setNode] = useState<HTMLElement | null>(null)
    const ref = useComposedRefs(forwardedRef, setNode)
    const getItems = TriggerCollection.useCollection()
    const prevMotionAttributeRef = useRef<string | null>(null)
    const { onItemDismiss } = context

    useEffect(() => {
      if (!node) return
      const handleClose = () => {
        onItemDismiss()
        onRootContentClose()
        if (node.contains(document.activeElement)) triggerRef.current?.focus()
      }
      node.addEventListener(ROOT_CONTENT_DISMISS, handleClose)
      return () => node.removeEventListener(ROOT_CONTENT_DISMISS, handleClose)
    }, [node, triggerRef, onItemDismiss, onRootContentClose])

    const motionAttribute = useMemo(() => {
      const values = getItems().map((item) => item.value)
      if (context.dir === "rtl") values.reverse()
      const index = values.indexOf(context.value)
      const prevIndex = values.indexOf(context.previousValue)
      const isSelected = value === context.value
      const wasSelected = prevIndex === values.indexOf(value)

      // Only the content entering and the one leaving move; the others keep what they had so their animation is not cut short
      if (!isSelected && !wasSelected) return prevMotionAttributeRef.current

      const attribute = (() => {
        if (index !== prevIndex) {
          if (isSelected && prevIndex !== -1) return index > prevIndex ? "from-end" : "from-start"
          if (wasSelected && index !== -1) return index > prevIndex ? "to-start" : "to-end"
        }
        // Entering from closed or leaving the menu entirely has no direction
        return null
      })()
      prevMotionAttributeRef.current = attribute
      return attribute
    }, [context.previousValue, context.value, context.dir, getItems, value])

    useDismissableLayer(node, {
      onEscapeKeyDown: composeEventHandlers(onEscapeKeyDown, () => {
        // So the pointer resting on the trigger does not reopen it
        wasEscapeCloseRef.current = true
      }),
      onPointerDownOutside: composeEventHandlers(onPointerDownOutside, (event) => {
        const target = event.target as Node
        // The trigger toggles on its own click, and the viewport around the content is part of the menu
        const isTrigger = getItems().some((item) => item.ref.current?.contains(target))
        if (isTrigger || context.viewport?.contains(target)) event.preventDefault()
      }),
      onFocusOutside: composeEventHandlers(onFocusOutside, (event) => {
        // Arrives late for the focus proxy: its own focus event has already moved the focus into the content
        if (node?.contains(document.activeElement)) return event.preventDefault()
        onContentFocusOutside()
        const root = context.rootRef.current
        const focusCameFromMenu = root?.contains(event.detail.originalEvent.relatedTarget as Node | null)
        // Only focus that leaves the menu dismisses it
        if (root?.contains(event.target as Node) || !focusCameFromMenu) event.preventDefault()
      }),
      onInteractOutside,
      onDismiss: () => node?.dispatchEvent(new Event(ROOT_CONTENT_DISMISS, { bubbles: true, cancelable: true })),
    })

    return (
      <FocusGroup asChild>
        <Primitive.div
          id={makeContentId(context.baseId, value)}
          aria-labelledby={makeTriggerId(context.baseId, value)}
          data-motion={motionAttribute}
          data-orientation={context.orientation}
          {...contentProps}
          ref={ref}
          onKeyDown={composeEventHandlers(contentProps.onKeyDown, (event) => {
            if (event.key !== "Tab" || event.altKey || event.ctrlKey || event.metaKey) return
            const candidates = getTabbableCandidates(event.currentTarget)
            const index = candidates.indexOf(document.activeElement as HTMLElement)
            const next = event.shiftKey ? candidates.slice(0, index).reverse() : candidates.slice(index + 1)
            // At the edges the proxy takes the focus and the browser carries on from there
            if (focusFirst(next)) event.preventDefault()
            else focusProxyRef.current?.focus()
          })}
        />
      </FocusGroup>
    )
  }
)

const NavigationMenuViewport = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const context = useNavigationMenuContext("NavigationMenuViewport")
  return (
    <Presence present={Boolean(context.value)}>
      <NavigationMenuViewportImpl {...props} ref={ref} />
    </Presence>
  )
})

const NavigationMenuViewportImpl = forwardRef<PrimitiveProps<"div">>(({ children, ...viewportProps }, forwardedRef) => {
  const context = useNavigationMenuContext("NavigationMenuViewport")
  const ref = useComposedRefs(forwardedRef, context.onViewportChange)
  const contents = useStore(context.contents)
  const [content, setContent] = useState<HTMLElement | null>(null)
  const size = useSize(content)
  const open = Boolean(context.value)
  // The viewport can be animating out, and its content stays until it is gone
  const activeContentValue = open ? context.value : context.previousValue

  return (
    <Primitive.div
      data-state={getOpenState(open)}
      data-orientation={context.orientation}
      {...viewportProps}
      ref={ref}
      style={mergeStyles(
        {
          // Nothing can be clicked while it animates out
          pointerEvents: open ? undefined : "none",
          "--radix-navigation-menu-viewport-width": size && `${size.width}px`,
          "--radix-navigation-menu-viewport-height": size && `${size.height}px`,
        },
        viewportProps.style
      )}
      onPointerEnter={composeEventHandlers(viewportProps.onPointerEnter, context.onContentEnter)}
      onPointerLeave={composeEventHandlers(viewportProps.onPointerLeave, whenMouse(context.onContentLeave))}
    >
      {children}
      {Array.from(contents, ([value, { ref: contentRef, ...props }]) => (
        <Presence key={value} present={activeContentValue === value}>
          <ViewportItem {...props} contentRef={contentRef} isActive={activeContentValue === value} onActiveContentChange={setContent} />
        </Presence>
      ))}
    </Primitive.div>
  )
})

// A component of its own to have a ref callback per item that keeps its identity
const ViewportItem = forwardRef<ContentImplProps & { contentRef?: Ref<any>; isActive: boolean; onActiveContentChange(node: HTMLElement): void }>(
  ({ contentRef, isActive, onActiveContentChange, ...props }, presenceRef) => {
    // The size follows the active content; a node that is leaving is not the one to measure
    const handleContentChange = useCallback(
      (node: HTMLElement | null) => {
        if (isActive && node) onActiveContentChange(node)
      },
      [isActive, onActiveContentChange]
    )
    const ref = useComposedRefs(presenceRef, contentRef, handleContentChange)
    return <NavigationMenuContentImpl {...props} ref={ref} />
  }
)

const FocusGroup = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const context = useNavigationMenuContext("FocusGroup")
  return (
    <FocusCollection.Provider>
      <FocusCollection.Slot>
        <Primitive.div dir={context.dir} {...props} ref={ref} />
      </FocusCollection.Slot>
    </FocusCollection.Provider>
  )
})

const ARROW_KEYS = ["ArrowRight", "ArrowLeft", "ArrowUp", "ArrowDown"]

// Unlike a roving tab index every trigger and link stays in the tab order; the arrows, Home and End move among them
const FocusGroupItem = forwardRef<PrimitiveProps<"button">>((props, ref) => {
  const getItems = FocusCollection.useCollection()
  const context = useNavigationMenuContext("FocusGroupItem")
  return (
    <FocusCollection.ItemSlot>
      <Primitive.button
        {...props}
        ref={ref}
        onKeyDown={composeEventHandlers(props.onKeyDown, (event) => {
          if (event.key !== "Home" && event.key !== "End" && !ARROW_KEYS.includes(event.key)) return
          let candidates = getItems().map((item) => item.ref.current!)
          if ([context.dir === "rtl" ? "ArrowRight" : "ArrowLeft", "ArrowUp", "End"].includes(event.key)) candidates.reverse()
          if (ARROW_KEYS.includes(event.key)) candidates = candidates.slice(candidates.indexOf(event.currentTarget) + 1)
          // Let the keydown finish before moving focus so the next item does not receive the same key event
          setTimeout(() => focusFirst(candidates))
          event.preventDefault()
        })}
      />
    </FocusCollection.ItemSlot>
  )
})

export {
  NavigationMenu as Root,
  NavigationMenuList as List,
  NavigationMenuItem as Item,
  NavigationMenuTrigger as Trigger,
  NavigationMenuContent as Content,
  NavigationMenuLink as Link,
  NavigationMenuIndicator as Indicator,
  NavigationMenuViewport as Viewport,
}
