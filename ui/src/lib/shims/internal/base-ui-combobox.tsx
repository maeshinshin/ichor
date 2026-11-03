import { createContext, type ComponentChildren, type CSSProperties, type Ref } from "preact"
import { useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks"
import { useComposedRefs } from "../../radix/internal/compose-refs"
import { createStrictContext } from "../../radix/internal/create-context"
import { useDismissableLayer } from "../../radix/internal/dismissable-layer"
import { getTabbableCandidates } from "../../radix/internal/focus-scope"
import { forwardRef } from "../../radix/internal/forward-ref"
import { hideOthers } from "../../radix/internal/modal-layer"
import * as Portal from "../../radix/internal/portal"
import { usePresence } from "../../radix/internal/presence"
import { mergeProps, useRender, type UseRenderComponentProps } from "./shadcn-react-use-render"
import { usePosition, resolveAnchor, type Align, type Anchor, type Side } from "./base-ui-combobox-position"

// Base UI 1.9.0 Combobox on Preact hooks: the same parts, roles, aria wiring, data attributes, keyboard and CSS variables.
// Only the parts and props upstream's ui/combobox.tsx and its example use, plus the controlled state around them.
// shortcut: items come from the root's `items` prop (flat or grouped); a ComboboxItem outside it renders and can be clicked, but keyboard
//   navigation and filtering do not reach it. No `createItems`, `filteredItems`, `limit`, `readOnly`, `grid`, `virtualized`, `inline`,
//   `modal`, `actionsRef`, `onItemHighlighted`, `loopFocus`, `keepHighlight`, `highlightItemOnHover`, Combobox.InputGroup, Row, Status,
//   Label, Icon, Arrow or Backdrop; `className` and `style` are not functions of the state; no `nativeButton` (button parts are native
//   buttons), `keepMounted` or portal `container`; no `index` on items; no browser-autofill matching or typeahead on the closed trigger.
// shortcut: no FloatingFocusManager: no aria-hidden on the page behind the open popup, focus guards only for the input in the popup (no
//   `aria-owns` span or portal id: screen readers do not get the popup placed after the trigger), and the popup's exit animation is
//   found from its CSS animation (as the Radix shims do), not from transitions (`data-starting-style`, `data-ending-style`).

type State = Record<string, unknown>
type Part<E extends "div" | "span" | "button" | "input", S extends State, Own = {}> = Omit<UseRenderComponentProps<E, S>, "ref" | "className" | "id" | keyof Own> & {
  ref?: Ref<any>
  className?: string
  id?: string
} & Own

type ChangeEventReason =
  | "trigger-press"
  | "input-press"
  | "outside-press"
  | "item-press"
  | "close-press"
  | "escape-key"
  | "list-navigation"
  | "focus-out"
  | "input-change"
  | "input-clear"
  | "clear-press"
  | "chip-remove-press"
  | "none"
type ChangeEventDetails = {
  reason: ChangeEventReason
  event: Event
  cancel(): void
  allowPropagation(): void
  readonly isCanceled: boolean
  readonly isPropagationAllowed: boolean
  trigger: Element | undefined
  /** With the reason `input-clear` in multiple mode: whether an item press caused it. */
  isItemPress?: boolean
}
type Group<Item = any> = { items: readonly Item[]; [key: string]: unknown }

const EMPTY: readonly never[] = []

// Base UI prefixes the ids it generates
const useBaseUiId = () => `base-ui-${useId()}`

function details(reason: ChangeEventReason, event?: Event, trigger?: Element, extra?: { isItemPress?: boolean }): ChangeEventDetails {
  let canceled = false
  let propagation = false
  return {
    reason,
    event: event ?? new Event("base-ui"),
    cancel: () => void (canceled = true),
    allowPropagation: () => void (propagation = true),
    get isCanceled() {
      return canceled
    },
    get isPropagationAllowed() {
      return propagation
    },
    trigger,
    ...extra,
  }
}

const serialize = (value: unknown): string => {
  if (value == null) return ""
  if (typeof value === "string") return value
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}
// `{ value, label }` items show their label and submit their value without further configuration
const stringifyLabel = (item: any, toLabel?: (item: any) => string): string => {
  if (toLabel && item != null) return toLabel(item) ?? ""
  if (item && typeof item === "object") {
    if ("label" in item && item.label != null) return String(item.label)
    if ("value" in item) return String(item.value)
  }
  return serialize(item)
}
const stringifyValue = (item: any, toValue?: (item: any) => string): string => {
  if (toValue && item != null) return toValue(item) ?? ""
  if (item && typeof item === "object" && "value" in item && "label" in item) return serialize(item.value)
  return serialize(item)
}

const isGroup = (item: any): item is Group => typeof item === "object" && item != null && Array.isArray(item.items)
const flatten = (items: readonly any[]): readonly any[] => (isGroup(items[0]) ? (items as readonly Group[]).flatMap((group) => group.items) : items)

let collator: Intl.Collator | undefined
// Accent- and case-insensitive substring match, like Base UI's default filter
const contains = (item: any, query: string, toLabel?: (item: any) => string) => {
  collator ??= new Intl.Collator(undefined, { usage: "search", sensitivity: "base", ignorePunctuation: true })
  const text = stringifyLabel(item, toLabel)
  for (let i = 0; i <= text.length - query.length; i++) if (collator.compare(text.slice(i, i + query.length), query) === 0) return true
  return false
}

type Equal = (item: any, value: any) => boolean
const equal = (eq: Equal, item: any, value: any) => (item == null || value == null ? Object.is(item, value) : eq(item, value))

const visuallyHidden: CSSProperties = {
  clipPath: "inset(50%)",
  overflow: "hidden",
  whiteSpace: "nowrap",
  border: 0,
  padding: 0,
  width: "1px",
  height: "1px",
  margin: "-1px",
  position: "absolute",
}

type RootContext = {
  rootId: string
  listId: string
  popupId: string
  itemId(index: number): string
  multiple: boolean
  disabled: boolean
  required: boolean
  autoHighlight: boolean
  openOnInputClick: boolean
  value: any
  hasValue: boolean
  filteredItems: readonly any[]
  flatValues: readonly any[]
  listEmpty: boolean
  inputValue: string
  open: boolean
  mounted: boolean
  activeIndex: number | null
  inputInPopup: boolean
  popupSide: Side | null
  isSelected(value: any): boolean
  indexOf(value: any): number
  toLabel(value: any): string
  customLabel: boolean
  hasInputValue: boolean
  items: readonly any[] | undefined
  setOpen(open: boolean, details: ChangeEventDetails): void
  setInputValue(value: string, details: ChangeEventDetails): void
  setSelectedValue(value: any, details: ChangeEventDetails): void
  setActive(index: number | null): void
  select(value: any, event: Event): void
  clickItem(index: number, event: Event): void
  removeValueAt(index: number, details: ChangeEventDetails): void
  highlightEdge(): void
  pointerModality: { current: boolean }
  touch: { current: boolean }
  forceScroll: { current: boolean }
  pointerDownItem: { current: Element | null }
  selectionEvent: { current: Event | null }
  inputRef: { current: HTMLInputElement | null }
  chipsRef: { current: HTMLElement | null }
  clearRef: { current: HTMLElement | null }
  dismissRef: { current: HTMLElement | null }
  triggerEl: HTMLElement | null
  inputEl: HTMLInputElement | null
  listEl: HTMLElement | null
  setTriggerEl(element: HTMLElement | null): void
  setInputEl(element: HTMLInputElement | null): void
  setListEl(element: HTMLElement | null): void
  setInputInPopup(inside: boolean): void
  // Where the input rendered, known while rendering when the input comes before the trigger in the tree
  placement: { current: boolean | null }
  setPopupSide(side: Side | null): void
  setPresent(present: boolean): void
  popupRef: { current: HTMLElement | null }
}
const [RootProvider, useRoot] = createStrictContext<RootContext>("Combobox.Root")
const useCombobox = (part: string) => useRoot(`Combobox.${part}`)

type PositionerContext = { side: Side; align: Align; positioned: boolean }
const PositionerContext = createContext<PositionerContext | undefined>(undefined)
const GroupContext = createContext<{ setLabelId(id: string | undefined): void; labelId: string | undefined; items?: readonly any[] } | undefined>(undefined)
const ItemContext = createContext<{ selected: boolean } | undefined>(undefined)
const ChipsContext = createContext<{ chips: Set<HTMLElement> } | undefined>(undefined)
const ChipContext = createContext<{ index(): number } | undefined>(undefined)

const byDocumentOrder = (a: Node, b: Node) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1)

/* -------------------------------------------------------------------------------------------------------------------------------- */

type RootProps<Value, Multiple extends boolean | undefined> = {
  children?: ComponentChildren
  /** Whether several items can be selected. @default false */
  multiple?: Multiple
  /** The items to filter and render: a flat array or an array of `{ items }` groups. */
  items?: readonly any[]
  value?: (Multiple extends true ? readonly Value[] : Value) | null
  defaultValue?: (Multiple extends true ? readonly Value[] : Value) | null
  onValueChange?(value: Multiple extends true ? Value[] : Value | null, eventDetails: ChangeEventDetails): void
  inputValue?: string
  defaultInputValue?: string
  onInputValueChange?(inputValue: string, eventDetails: ChangeEventDetails): void
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?(open: boolean, eventDetails: ChangeEventDetails): void
  /** Highlight the first match while the user types. @default false */
  autoHighlight?: boolean
  disabled?: boolean
  required?: boolean
  /** Names the field in a submitted form. */
  name?: string
  form?: string
  id?: string
  /** Opens the popup when the input is pressed. @default true */
  openOnInputClick?: boolean
  itemToStringLabel?(value: Value): string
  itemToStringValue?(value: Value): string
  isItemEqualToValue?(itemValue: Value, value: Value): boolean
  /** `null` turns filtering off, e.g. when the items are already filtered on a server. */
  filter?: null | ((item: any, query: string, itemToString?: (item: any) => string) => boolean)
}

function ComboboxRoot<Value = any, Multiple extends boolean | undefined = false>(props: RootProps<Value, Multiple>) {
  const {
    children,
    items,
    autoHighlight = false,
    disabled = false,
    required = false,
    name,
    form,
    id: idProp,
    openOnInputClick = true,
    itemToStringLabel,
    itemToStringValue,
    isItemEqualToValue = Object.is,
    filter: filterProp,
  } = props
  const multiple = !!props.multiple
  const generatedId = useBaseUiId()
  const rootId = idProp ?? generatedId
  const listId = useId()
  const toLabel = (value: any) => stringifyLabel(value, itemToStringLabel as ((item: any) => string) | undefined)
  const eq = isItemEqualToValue as Equal

  const [valueState, setValueState] = useState<any>(props.defaultValue ?? (multiple ? EMPTY : null))
  const value: any = (props.value !== undefined ? props.value : valueState) ?? (multiple ? EMPTY : null)
  const hasValue = multiple ? value.length > 0 : value != null
  const selectedLabel = multiple ? "" : toLabel(value)

  const hasInputValue = props.inputValue !== undefined || props.defaultInputValue !== undefined
  const [inputState, setInputState] = useState(() => props.defaultInputValue ?? (multiple ? "" : toLabel(value)))
  const inputValue = String(props.inputValue ?? inputState)
  const [openState, setOpenState] = useState(props.defaultOpen ?? false)
  const open = props.open ?? openState

  const [activeIndex, setActive] = useState<number | null>(null)
  const [queryChanged, setQueryChanged] = useState(false)
  const [closeQuery, setCloseQuery] = useState<string | null>(null)
  const [inputInPopup, setInputInPopup] = useState(true)
  const placement = useRef<boolean | null>(null)
  const [popupSide, setPopupSide] = useState<Side | null>(null)
  const [triggerEl, setTriggerEl] = useState<HTMLElement | null>(null)
  const [inputEl, setInputElement] = useState<HTMLInputElement | null>(null)
  const [listEl, setListEl] = useState<HTMLElement | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const chipsRef = useRef<HTMLElement | null>(null)
  const clearRef = useRef<HTMLElement | null>(null)
  const dismissRef = useRef<HTMLElement | null>(null)
  const popupRef = useRef<HTMLElement | null>(null)
  // How the popup was opened, which decides where the highlight starts
  const openedBy = useRef<{ key?: string; typed?: boolean } | null>(null)
  const pointerModality = useRef(true)
  const touch = useRef(false)
  const pointerDownItem = useRef<Element | null>(null)
  const selectionEvent = useRef<Event | null>(null)
  const forceScroll = useRef(false)
  const edge = useRef<string | null>(null)

  // Typing filters; with the selection's own label in the input every item stays visible, so the list can be browsed
  const query = !open && closeQuery !== null ? closeQuery : inputValue.trim()
  const bypass = !multiple && !queryChanged && query !== "" && selectedLabel.length === query.length && contains(selectedLabel, query)
  const filterQuery = bypass ? "" : query
  const grouped = !!items && isGroup(items[0])
  const filteredItems = useMemo(() => {
    if (!items) return EMPTY
    if (filterQuery === "") return items
    const keep = (item: any) => (filterProp === null ? true : filterProp ? filterProp(item, filterQuery, itemToStringLabel as any) : item != null && contains(item, filterQuery, itemToStringLabel as any))
    return grouped
      ? (items as readonly Group[]).map((group) => ({ ...group, items: group.items.filter(keep) })).filter((group) => group.items.length > 0)
      : items.filter(keep)
  }, [items, grouped, filterQuery, filterProp, itemToStringLabel])
  const flatValues = useMemo(() => flatten(filteredItems), [filteredItems])
  const indexOf = (itemValue: any) => flatValues.findIndex((item) => equal(eq, item, itemValue))
  const isSelected = (itemValue: any) => (multiple ? (value as readonly any[]).some((selected) => equal(eq, itemValue, selected)) : equal(eq, itemValue, value))
  const found = flatValues.findIndex(isSelected)
  const selectedIndex = found === -1 ? null : found
  const listEmpty = filteredItems.length === 0

  // Whether the popup is still on the page for its exit animation
  const [present, setPresent] = useState(false)
  const mounted = open || present

  // Ref callbacks that get composed must keep their identity, or every render would detach and attach them
  const setInputEl = useCallback((element: HTMLInputElement | null) => {
    inputRef.current = element
    setInputElement(element)
  }, [])

  const setInputValue = (next: string, eventDetails: ChangeEventDetails) => {
    props.onInputValueChange?.(next, eventDetails)
    if (eventDetails.isCanceled) return
    if (eventDetails.reason === "input-change") {
      if (open && closeQuery !== null) setCloseQuery(null)
      const event = eventDetails.event as InputEvent
      const typed = event.type === "compositionend" || (event.inputType != null && event.inputType !== "" && event.inputType !== "insertReplacementText")
      if (typed) {
        const hasQuery = next.trim() !== ""
        if (hasQuery) setQueryChanged(true)
        if (listEl) listEl.scrollTop = 0
        // Typing hands the highlight back to the input, unless the first match is to be highlighted
        if (!autoHighlight) setActive(null)
        else if (hasQuery) setActive(0)
      }
    }
    setInputState(next)
  }

  const setSelectedValue = (next: any, eventDetails: ChangeEventDetails) => {
    ;(props.onValueChange as ((value: any, details: ChangeEventDetails) => void) | undefined)?.(next, eventDetails)
    if (eventDetails.isCanceled) return
    setValueState(next)
    if (!multiple && !inputInPopup) setInputValue(toLabel(next), details(eventDetails.reason, eventDetails.event))
  }

  const setOpen = (next: boolean, eventDetails: ChangeEventDetails) => {
    if (open === next) return
    props.onOpenChange?.(next, eventDetails)
    if (eventDetails.isCanceled) return
    if (next) openedBy.current = { typed: eventDetails.reason === "input-change", key: eventDetails.reason === "list-navigation" ? (eventDetails.event as KeyboardEvent).key : undefined }
    if (next && closeQuery !== null) setCloseQuery(null)
    if (!next && queryChanged) {
      // The typed query keeps filtering while the popup animates out
      setCloseQuery(query)
      if (!multiple && query === "") setQueryChanged(false)
      if (multiple && inputInPopup) setActive(null)
      if (multiple && !inputInPopup) setInputValue("", details("input-clear", eventDetails.event, undefined, { isItemPress: eventDetails.reason === "item-press" }))
    }
    setOpenState(next)
  }

  const highlightEdge = () => {
    const key = edge.current
    edge.current = null
    if (!key) return
    const enabled = (i: number) => document.getElementById(`${rootId}-${i}`)?.getAttribute("aria-disabled") !== "true"
    const order = flatValues.map((_, i) => i)
    forceScroll.current = true
    setActive((key === "ArrowUp" ? order.reverse() : order).find(enabled) ?? null)
  }

  const select = (itemValue: any, event: Event) => {
    const eventDetails = details("item-press", event)
    if (multiple) {
      const current = value as readonly any[]
      const nextValue = isSelected(itemValue) ? current.filter((selected) => !equal(eq, itemValue, selected)) : [...current, itemValue]
      setSelectedValue(nextValue, eventDetails)
      if (eventDetails.isCanceled) return
      // Picking after a search starts the next one; without one the popup stays open for more picks
      if (!inputRef.current?.value.trim()) return
      if (inputInPopup) setInputValue("", details("input-clear", event, undefined, { isItemPress: true }))
      else setOpen(false, eventDetails)
    } else {
      setSelectedValue(itemValue, eventDetails)
      if (!eventDetails.isCanceled) setOpen(false, eventDetails)
    }
  }

  // Enter commits the highlighted item the way a click does, so the item's own onClick runs too
  const clickItem = (index: number, event: Event) => {
    const element = document.getElementById(`${rootId}-${index}`)
    if (!element) return
    selectionEvent.current = event
    element.click()
    selectionEvent.current = null
  }

  const removeValueAt = (index: number, eventDetails: ChangeEventDetails) => {
    const current = value as readonly any[]
    setSelectedValue(
      current.filter((_, i) => i !== index),
      eventDetails
    )
  }

  // The popup was closed (and its exit animation is over): the next open starts fresh
  const cleanup = () => {
    setQueryChanged(false)
    setCloseQuery(null)
    setActive(null)
    // The search starts over, and an input outside the popup shows the selection again
    const text = multiple || inputInPopup ? "" : selectedLabel
    if (inputValue !== text) setInputValue(text, details(text === "" ? "input-clear" : "none"))
  }
  const wasMounted = useRef(mounted)
  useLayoutEffect(() => {
    if (wasMounted.current && !mounted) cleanup()
    wasMounted.current = mounted
  }, [mounted])

  // A value that changed from outside (or by picking an item) shows in the input again, unless the input is controlled
  const syncedLabel = useRef(selectedLabel)
  useLayoutEffect(() => {
    if (syncedLabel.current === selectedLabel) return
    syncedLabel.current = selectedLabel
    if (!multiple && !hasInputValue && !inputInPopup && inputValue !== selectedLabel) setInputValue(selectedLabel, details("none"))
  }, [selectedLabel])

  // Opening starts on the selected item; opened with an arrow key and nothing selected, on the first or last enabled item
  const wasOpen = useRef(open)
  useLayoutEffect(() => {
    const before = wasOpen.current
    wasOpen.current = open
    if (open === before) return
    if (!open) return setActive(null)
    const by = openedBy.current
    openedBy.current = null
    let next: number | null = null
    if (by?.typed) next = autoHighlight && query !== "" ? 0 : null
    else if (selectedIndex != null) next = selectedIndex
    // With an arrow key and nothing selected the first or last item that is not disabled, found once the items have rendered
    else if (by?.key) edge.current = by.key
    forceScroll.current = next != null
    setActive(next)
  }, [open])

  // The list shrank below the highlight
  useLayoutEffect(() => {
    if (activeIndex != null && activeIndex >= flatValues.length) setActive(null)
  })

  // Focus goes back to the trigger when a popup that held the input closes
  useLayoutEffect(() => {
    if (open || !inputInPopup || !triggerEl) return
    const active = document.activeElement
    if (!active || active === document.body || popupRef.current?.contains(active)) triggerEl.focus({ preventScroll: true })
  }, [open])

  const context: RootContext = {
    rootId,
    listId,
    popupId: `${rootId}-popup`,
    itemId: (index) => `${rootId}-${index}`,
    multiple,
    disabled,
    required,
    autoHighlight,
    openOnInputClick,
    value,
    hasValue,
    filteredItems,
    flatValues,
    listEmpty,
    inputValue,
    open,
    mounted,
    activeIndex,
    inputInPopup,
    popupSide,
    isSelected,
    indexOf,
    toLabel,
    customLabel: !!itemToStringLabel,
    hasInputValue,
    items,
    setOpen,
    setInputValue,
    setSelectedValue,
    setActive,
    select,
    clickItem,
    removeValueAt,
    highlightEdge,
    pointerModality,
    touch,
    forceScroll,
    pointerDownItem,
    selectionEvent,
    inputRef,
    chipsRef,
    clearRef,
    dismissRef,
    triggerEl,
    inputEl,
    listEl,
    setTriggerEl,
    setInputEl,
    setListEl,
    setInputInPopup,
    placement,
    setPopupSide,
    setPresent,
    popupRef,
  }

  // The field value of a form: the serialized selection. A multiple selection is one hidden input per value.
  const fieldValue = (item: any) => stringifyValue(item, itemToStringValue as ((item: any) => string) | undefined)
  return (
    <RootProvider value={context}>
      {children}
      <input
        aria-hidden="true"
        tabIndex={-1}
        id={name == null ? `${rootId}-hidden-input` : undefined}
        name={multiple ? undefined : name}
        form={form}
        disabled={disabled}
        required={required && !(multiple && hasValue)}
        value={multiple ? "" : fieldValue(value)}
        // The browser focuses a failing required field: the visible input takes over
        onFocus={() => (inputRef.current ?? triggerEl)?.focus()}
        style={{ ...visuallyHidden, margin: 0, position: "fixed", top: 0, left: 0 }}
      />
      {multiple && name != null
        ? (value as readonly any[]).map((item) => <input key={fieldValue(item)} type="hidden" form={form} name={name} value={fieldValue(item)} disabled={disabled} />)
        : null}
    </RootProvider>
  )
}

type Reason = ChangeEventReason
type Details = ChangeEventDetails
namespace ComboboxRoot {
  export type Props<Value = any, Multiple extends boolean | undefined = false> = RootProps<Value, Multiple>
  export type ChangeEventReason = Reason
  export type ChangeEventDetails = Details
  export type OpenChangeEventDetails = Details
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type ValueProps = {
  /** Formats the selection; a function receives the selected value (an array when `multiple`). */
  children?: ComponentChildren | ((selectedValue: any) => ComponentChildren)
  /** Shown while nothing is selected. */
  placeholder?: ComponentChildren
}

function labelOfSelection(ctx: RootContext): ComponentChildren {
  const label = (value: any): ComponentChildren => {
    if (!ctx.customLabel) {
      // A label can be markup: show it as is. A plain value is looked up in the items, which pair it with a label.
      if (value && typeof value === "object" && value.label != null) return value.label
      if (ctx.items && value != null && typeof value !== "object") {
        const match = flatten(ctx.items).find((item) => item && typeof item === "object" && item.value === value)
        if (match?.label != null) return match.label
      }
    }
    return ctx.toLabel(value)
  }
  return ctx.multiple ? (ctx.value as readonly any[]).flatMap((value, i) => (i ? [", ", label(value)] : [label(value)])) : label(ctx.value)
}

function ComboboxValue({ children, placeholder }: ValueProps) {
  const ctx = useCombobox("Value")
  if (typeof children === "function") return <>{children(ctx.value)}</>
  if (children != null) return <>{children}</>
  if (!ctx.hasValue && placeholder != null) return <>{placeholder}</>
  return <>{labelOfSelection(ctx)}</>
}
namespace ComboboxValue {
  export type Props = ValueProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

const openAttributes = (names: Record<string, string>) => (open: boolean) => (open ? names : null)
const pressableOpen = { open: openAttributes({ "data-popup-open": "", "data-pressed": "" }) }
const popupOpen = { open: (open: boolean) => (open ? { "data-open": "" } : { "data-closed": "" }) }

type TriggerState = { open: boolean; disabled: boolean; popupSide: Side | null; listEmpty: boolean; placeholder: boolean }
type TriggerProps = Part<"button", TriggerState, { disabled?: boolean }>

const ComboboxTrigger = forwardRef<TriggerProps>(({ render, disabled: disabledProp = false, id: idProp, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Trigger")
  const disabled = ctx.disabled || disabledProp
  // Until the input shows where it is the trigger is the combobox itself, as in Base UI. An input that rendered first says so already,
  // which also settles the markup rendered on the server.
  const inside = ctx.placement.current ?? ctx.inputInPopup
  const ref = useComposedRefs(forwardedRef, ctx.setTriggerEl)
  const toggle = (event: Event) => ctx.setOpen(!ctx.open, details("trigger-press", event))
  const state: TriggerState = { open: ctx.open, disabled, popupSide: ctx.mounted && ctx.listEl ? ctx.popupSide : null, listEmpty: ctx.listEmpty, placeholder: !ctx.hasValue }
  return useRender({
    defaultTagName: "button",
    render,
    state,
    stateAttributesMapping: pressableOpen,
    props: mergeProps(
      {
        id: inside ? (idProp ?? ctx.rootId) : idProp,
        type: "button",
        tabIndex: inside ? 0 : -1,
        role: inside ? "combobox" : undefined,
        "aria-expanded": String(ctx.open),
        "aria-haspopup": inside ? "dialog" : "listbox",
        "aria-controls": ctx.open ? (inside ? ctx.popupId : ctx.listEl?.id) : undefined,
        "aria-required": inside && ctx.required ? "true" : undefined,
        disabled: disabled || undefined,
        onPointerDown: (event: PointerEvent) => void (ctx.touch.current = event.pointerType === "touch"),
        onMouseDown(event: MouseEvent) {
          if (disabled) return
          // The input keeps the focus while the popup is used
          ctx.inputRef.current?.focus({ preventScroll: true })
          if (!inside) event.preventDefault()
          toggle(event)
        },
        // A key press (or assistive technology) clicks without a mouse down
        onClick(event: MouseEvent) {
          if (!disabled && event.detail === 0) toggle(event)
        },
        onKeyDown(event: KeyboardEvent) {
          if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return
          event.preventDefault()
          event.stopPropagation()
          ctx.setOpen(true, details("list-navigation", event))
          ctx.inputRef.current?.focus({ preventScroll: true })
        },
      },
      { ...rest, ref }
    ),
  })
})
namespace ComboboxTrigger {
  export type State = TriggerState
  export type Props = TriggerProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type ClearState = { disabled: boolean; visible: boolean; open: boolean }
type ClearProps = Part<"button", ClearState, { disabled?: boolean }>

const ComboboxClear = forwardRef<ClearProps>(({ render, disabled: disabledProp = false, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Clear")
  const disabled = ctx.disabled || disabledProp
  const ref = useComposedRefs(forwardedRef, (element: HTMLElement | null) => void (ctx.clearRef.current = element))
  if (!ctx.hasValue) return null
  const state: ClearState = { disabled, visible: true, open: ctx.open }
  return useRender({
    defaultTagName: "button",
    render,
    state,
    stateAttributesMapping: { open: openAttributes({ "data-popup-open": "" }) },
    props: mergeProps(
      {
        type: "button",
        tabIndex: -1,
        children: "x",
        disabled: disabled || undefined,
        // Pressing it must not take the focus from the input
        onMouseDown: (event: MouseEvent) => event.preventDefault(),
        onClick(event: MouseEvent) {
          if (disabled) return
          ctx.setInputValue("", details("clear-press", event))
          ctx.setSelectedValue(ctx.multiple ? [] : null, details("clear-press", event))
          ctx.setActive(null)
          ctx.inputRef.current?.focus()
        },
      },
      { ...rest, ref }
    ),
  })
})
namespace ComboboxClear {
  export type State = ClearState
  export type Props = ClearProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

const DISMISS = { ...visuallyHidden }

function DismissButton() {
  const ctx = useCombobox("Popup")
  // For screen readers that cannot press Escape: a button next to the popup that closes it
  return <span ref={(element) => void (ctx.dismissRef.current = element)} role="button" aria-label="Dismiss" style={DISMISS} onClick={(event) => ctx.setOpen(false, details("close-press", event))} />
}

type InputState = { open: boolean; disabled: boolean; popupSide: Side | null; listEmpty: boolean }
type InputProps = Part<"input", InputState, { disabled?: boolean }>

const ComboboxInput = forwardRef<InputProps>(({ render, disabled: disabledProp = false, id: idProp, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Input")
  const insidePopup = !!useContext(PositionerContext)
  const chips = useContext(ChipsContext)
  ctx.placement.current = insidePopup
  const generatedId = useBaseUiId()
  const disabled = ctx.disabled || disabledProp
  const [composing, setComposing] = useState<string | null>(null)
  const isComposing = useRef(false)
  const ref = useComposedRefs(forwardedRef, ctx.setInputEl)

  const { setInputInPopup, setInputValue, inputRef, hasInputValue } = ctx
  useLayoutEffect(() => {
    setInputInPopup(insidePopup)
    // The search field in a popup starts empty, whatever is selected
    if (insidePopup && !hasInputValue) setInputValue("", details("none"))
  }, [insidePopup])
  // After the press that opened the popup is over: the browser focuses the trigger as the default action of that press
  useLayoutEffect(() => {
    if (!insidePopup) return
    // A touch opens the popup without the on-screen keyboard: the popup takes the focus, not the field
    const timer = setTimeout(() => (ctx.touch.current ? ctx.popupRef.current : inputRef.current)?.focus({ preventScroll: true }))
    return () => clearTimeout(timer)
  }, [insidePopup])

  const maybeOpen = (event: Event, value: string, typed: boolean) => {
    if (disabled || !value.trim() || !typed) return
    ctx.setOpen(true, details("input-change", event))
  }

  const state: InputState = { open: ctx.open, disabled, popupSide: ctx.mounted && ctx.listEl ? ctx.popupSide : null, listEmpty: ctx.listEmpty }
  const element = useRender({
    defaultTagName: "input",
    render,
    state,
    stateAttributesMapping: pressableOpen,
    props: mergeProps(
      {
        id: idProp ?? (insidePopup ? generatedId : ctx.rootId),
        autocomplete: "off",
        // Preact writes a prop through the property when the element has one, where "false" and "off" read as true: the camelCase name
        // is no property (an attribute), and a boolean does the same for spellcheck
        spellcheck: false,
        autoCorrect: "off",
        autocapitalize: "none",
        role: "combobox",
        "aria-expanded": String(ctx.open),
        "aria-haspopup": "listbox",
        "aria-autocomplete": "list",
        "aria-controls": ctx.open ? ctx.listEl?.id : undefined,
        "aria-activedescendant": ctx.open && ctx.activeIndex != null ? ctx.itemId(ctx.activeIndex) : undefined,
        "aria-required": ctx.required ? "true" : undefined,
        value: composing ?? ctx.inputValue,
        disabled: disabled || undefined,
        onPointerDown: (event: PointerEvent) => void (ctx.touch.current = event.pointerType === "touch"),
        onMouseDown(event: MouseEvent) {
          if (!disabled && ctx.openOnInputClick && !ctx.open) ctx.setOpen(true, details("input-press", event))
        },
        onCompositionStart(event: CompositionEvent) {
          isComposing.current = true
          setComposing((event.currentTarget as HTMLInputElement).value)
        },
        // The list is only filtered by the finished composition
        onCompositionEnd(event: CompositionEvent) {
          isComposing.current = false
          setComposing(null)
          ctx.setInputValue((event.currentTarget as HTMLInputElement).value, details("input-change", event))
        },
        onInput(event: InputEvent) {
          const input = event.currentTarget as HTMLInputElement
          if (isComposing.current) {
            setComposing(input.value)
            maybeOpen(event, input.value, true)
            return
          }
          const change = details("input-change", event)
          ctx.setInputValue(input.value, change)
          if (change.isCanceled) {
            input.value = ctx.inputValue
            return
          }
          if (input.value === "" && !ctx.inputInPopup) {
            if (!ctx.multiple) ctx.setSelectedValue(null, details("input-clear", event))
            if (!ctx.openOnInputClick) ctx.setOpen(false, details("input-clear", event))
          }
          // Autofill reports no input type (Chrome) or a replacement (Firefox) and must not open the popup
          maybeOpen(event, input.value, !!event.inputType && event.inputType !== "insertReplacementText")
        },
        onKeyDown(event: KeyboardEvent) {
          const modified = event.ctrlKey || event.shiftKey || event.altKey || event.metaKey
          const input = event.currentTarget as HTMLInputElement
          if (disabled || (event as any).which === 229) return
          if (event.key === "Tab" && ctx.open && !ctx.inputInPopup) {
            // The focus leaves, and nothing in the popup can take it
            ctx.setOpen(false, details("focus-out", event))
            return
          }
          if (!modified && (event.key === "Home" || event.key === "End")) {
            // The caret moves; the list is navigated with the arrow keys
            event.preventDefault()
            event.stopPropagation()
            const caret = event.key === "Home" ? 0 : input.value.length
            input.setSelectionRange(caret, caret)
            input.scrollLeft = event.key === "Home" ? 0 : input.scrollWidth - input.clientWidth
            return
          }
          if (!modified && event.key === "Escape" && !ctx.mounted && !event.defaultPrevented) {
            // Closed: Escape clears, and a parent dialog only closes once there is nothing left to clear
            const empty = ctx.multiple ? ctx.value.length === 0 : ctx.value === null
            const clear = details("escape-key", event)
            ctx.setInputValue("", clear)
            ctx.setSelectedValue(ctx.multiple ? [] : null, clear)
            if (!empty && !clear.isPropagationAllowed) event.stopPropagation()
            return
          }
          if (!modified && chips) {
            if (event.key === "Backspace" && input.value === "" && ctx.multiple && ctx.value.length > 0) {
              ctx.setActive(null)
              ctx.removeValueAt(ctx.value.length - 1, details("none", event))
              return
            }
            // From the start of the text, ArrowLeft moves on to the last chip
            if (event.key === "ArrowLeft" && (input.selectionStart ?? 0) === 0 && ctx.multiple && ctx.value.length > 0) {
              const last = [...chips.chips].sort(byDocumentOrder).at(-1)
              if (last) {
                event.preventDefault()
                last.focus()
                return
              }
            }
          }
          if (!modified && event.key === "Enter" && ctx.open) {
            if (ctx.activeIndex === null) {
              // Nothing highlighted: the form may be submitted
              ctx.setOpen(false, details("none", event))
              return
            }
            event.preventDefault()
            event.stopPropagation()
            ctx.clickItem(ctx.activeIndex, event)
            return
          }
          if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return
          event.preventDefault()
          event.stopPropagation()
          ctx.pointerModality.current = false
          if (!ctx.open) {
            ctx.setOpen(true, details("list-navigation", event))
            return
          }
          // The input is part of the loop: past either end the highlight returns to it (unless the first match stays highlighted)
          const length = ctx.flatValues.length
          const current = ctx.activeIndex ?? -1
          const leave = !ctx.autoHighlight
          let next: number | null = null
          if (length > 0 && event.key === "ArrowUp") next = current > 0 ? current - 1 : current === 0 && leave ? null : length - 1
          else if (length > 0) next = current < length - 1 ? current + 1 : leave ? null : 0
          ctx.setActive(next)
        },
      },
      { ...rest, ref }
    ),
  })
  return (
    <>
      {ctx.open && !insidePopup ? <DismissButton /> : null}
      {element}
    </>
  )
})
namespace ComboboxInput {
  export type State = InputState
  export type Props = InputProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type PortalProps = Part<"div", {}, {}>

// Base UI's focus guards for a non-modal popup (the input inside it): the portal sits at the end of the page, so two guards after the
// trigger send Tab into it and two around the popup send the focus back, keeping the popup open for Shift+Tab to the trigger and closing it
// (through the dismissable layer) when the focus lands outside
const GUARD = "[data-base-ui-focus-guard]"
const guardStyle: CSSProperties = { ...visuallyHidden, margin: 0, position: "fixed", top: 0, left: 0 }

function focusGuards(ctx: RootContext) {
  const portalNode = () => ctx.popupRef.current?.closest("[data-base-ui-portal]") ?? null
  const tabbables = (inside: boolean) => getTabbableCandidates(document.body).filter((el) => !el.matches(GUARD) && !!portalNode()?.contains(el) === inside)
  const enter = (last: boolean) => (last ? tabbables(true).at(-1) : tabbables(true)[0])?.focus({ preventScroll: true })
  const inside = (event: FocusEvent) => !!portalNode()?.contains(event.relatedTarget as Node)
  const guard = (type: "inside" | "outside", onFocus: (event: FocusEvent) => void) => (
    <span aria-hidden="true" data-base-ui-focus-guard="" data-type={type} tabIndex={0} style={guardStyle} onFocus={onFocus} />
  )
  return {
    enter,
    guard,
    before: () => guard("inside", (event) => (inside(event) ? ctx.triggerEl?.focus({ preventScroll: true }) : enter(false))),
    after: () =>
      guard("inside", (event) => {
        if (!inside(event)) return enter(true)
        const next = ctx.triggerEl && tabbables(false).find((el) => ctx.triggerEl!.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING)
        next?.focus({ preventScroll: true })
      }),
  }
}

const ComboboxPortal = forwardRef<PortalProps>(({ render, children, ...rest }, ref) => {
  const ctx = useCombobox("Portal")
  if (!ctx.mounted) return null
  const portal = (
    <Portal.Root>{useRender({ defaultTagName: "div", render, props: mergeProps({ "data-base-ui-portal": "", children }, { ...rest, ref }) })}</Portal.Root>
  )
  if (!ctx.inputInPopup || !ctx.open) return portal
  const { guard, enter } = focusGuards(ctx)
  return (
    <>
      {guard("outside", () => enter(false))}
      {portal}
      {guard("outside", () => enter(true))}
    </>
  )
})
namespace ComboboxPortal {
  export type State = {}
  export type Props = PortalProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type PositionerState = { open: boolean; side: Side; align: Align; empty: boolean }
type PositionerProps = Part<
  "div",
  PositionerState,
  {
    /** The element the popup is placed against: the input (or the trigger when the input is in the popup) by default. */
    anchor?: Anchor
    /** @default "bottom" */
    side?: Side
    /** @default "center" */
    align?: Align
    sideOffset?: number
    alignOffset?: number
    collisionPadding?: number
  }
>

const ComboboxPositioner = forwardRef<PositionerProps>(
  ({ render, anchor, side = "bottom", align = "center", sideOffset = 0, alignOffset = 0, collisionPadding, ...rest }, forwardedRef) => {
    const ctx = useCombobox("Positioner")
    const position = usePosition({
      anchor: () => resolveAnchor(anchor) ?? (ctx.inputInPopup ? ctx.triggerEl : ctx.inputEl),
      side,
      align,
      sideOffset,
      alignOffset,
      collisionPadding,
    })
    const [element, setElement] = useState<HTMLElement | null>(null)
    const ref = useComposedRefs(forwardedRef, setElement, position.setElement)
    const { setPopupSide } = ctx
    useLayoutEffect(() => {
      setPopupSide(position.side)
      return () => setPopupSide(null)
    }, [position.side])

    // With the input outside the popup, Base UI hides the rest of the page from assistive technology while the list is open; the page
    // stays usable (a press outside still closes it). With the input in the popup it is a non-modal dialog and hides nothing.
    useLayoutEffect(() => {
      if (ctx.open && element && ctx.placement.current === false) return hideOthers("aria-hidden", element, ctx.inputEl, ctx.dismissRef.current)
    }, [ctx.open, element, ctx.inputEl])

    const anchors = () => [ctx.inputEl, ctx.triggerEl, ctx.chipsRef.current, ctx.clearRef.current]
    useDismissableLayer(ctx.open ? element : null, {
      onEscapeKeyDown(event) {
        // The input must not also treat this key as a request to clear
        event.preventDefault()
        ctx.setOpen(false, details("escape-key", event))
      },
      onPointerDownOutside(event) {
        if (anchors().some((anchorElement) => anchorElement?.contains(event.target as Node))) return event.preventDefault()
        ctx.setOpen(false, details("outside-press", event.detail.originalEvent))
      },
      onFocusOutside(event) {
        if (anchors().some((anchorElement) => anchorElement?.contains(event.target as Node)) || (event.target as Element).matches?.(GUARD)) return event.preventDefault()
        ctx.setOpen(false, details("focus-out", event.detail.originalEvent))
      },
    })

    const state: PositionerState = { open: ctx.open, side: position.side, align: position.align, empty: ctx.listEmpty }
    const context = useMemo(() => ({ side: position.side, align: position.align, positioned: position.positioned }), [position.side, position.align, position.positioned])
    return (
      <PositionerContext.Provider value={context}>
        {useRender({
          defaultTagName: "div",
          render,
          state,
          stateAttributesMapping: popupOpen,
          props: mergeProps(
            { role: "presentation", style: { ...position.style, ...(ctx.open ? {} : { pointerEvents: "none" }) } as CSSProperties },
            { ...rest, ref }
          ),
        })}
      </PositionerContext.Provider>
    )
  }
)
namespace ComboboxPositioner {
  export type State = PositionerState
  export type Props = PositionerProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type PopupState = { open: boolean; side: Side; align: Align; empty: boolean }
type PopupProps = Part<"div", PopupState>

const ComboboxPopup = forwardRef<PopupProps>(({ render, id: idProp, style, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Popup")
  const position = useContext(PositionerContext)
  if (!position) throw new Error("`Combobox.Popup` must be used within `Combobox.Positioner`")
  const [element, setElement] = useState<HTMLElement | null>(null)
  // The popup reports whether it is still on the page for its exit animation. That has to be decided here, after it rendered as closed.
  const presence = usePresence(ctx.open)
  const { setPresent } = ctx
  useLayoutEffect(() => {
    setPresent(presence.isPresent)
  }, [presence.isPresent])
  useLayoutEffect(() => () => setPresent(false), [])
  const ref = useComposedRefs(forwardedRef, presence.ref, ctx.popupRef, setElement)
  const { inputRef, inputInPopup, listEl } = ctx

  // Focus that lands on the popup itself or the list (a click on the scrollbar) goes to the input
  useEffect(() => {
    if (!element || inputInPopup) return
    const redirect = (event: FocusEvent) => {
      if (!ctx.touch.current && (event.target === element || listEl?.contains(event.target as Node))) inputRef.current?.focus({ preventScroll: true })
    }
    element.addEventListener("focusin", redirect)
    return () => element.removeEventListener("focusin", redirect)
  }, [element, inputInPopup, listEl])

  const state: PopupState = { open: ctx.open, side: position.side, align: position.align, empty: ctx.listEmpty }
  const guards = inputInPopup ? focusGuards(ctx) : null
  return (
    <>
      {guards?.before()}
      {useRender({
        defaultTagName: "div",
        render,
        state,
        stateAttributesMapping: popupOpen,
        props: mergeProps(
          {
            id: idProp ?? (inputInPopup ? ctx.popupId : undefined),
            role: inputInPopup ? "dialog" : "presentation",
            tabIndex: -1,
            "data-base-ui-focusable": "",
            // Animations (the exit one too) wait for the final side, which they slide in from
            style: { ...(style as CSSProperties), ...(position.positioned ? {} : { animation: "none" }) },
            onPointerMove: () => void (ctx.pointerModality.current = true),
          },
          { ...rest, ref }
        ),
      })}
      {guards?.after()}
      {inputInPopup ? null : <DismissButton />}
    </>
  )
})
namespace ComboboxPopup {
  export type State = PopupState
  export type Props = PopupProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type ListProps = Omit<Part<"div", { empty: boolean }>, "children"> & { children?: ComponentChildren | ((item: any, index: number) => ComponentChildren) }

const ComboboxList = forwardRef<ListProps>(({ render, children, id: idProp, ...rest }, forwardedRef) => {
  const ctx = useCombobox("List")
  const ref = useComposedRefs(forwardedRef, ctx.setListEl)
  const { highlightEdge } = ctx
  // The items have rendered by now: they are this element's children
  useLayoutEffect(() => highlightEdge(), [])
  // The highlight stays in view when the keys move it, and the selection is brought into view when the popup opens
  const { activeIndex, open, pointerModality, forceScroll, rootId } = ctx
  useLayoutEffect(() => {
    if (activeIndex == null || !open || (pointerModality.current && !forceScroll.current)) return
    forceScroll.current = false
    document.getElementById(`${rootId}-${activeIndex}`)?.scrollIntoView?.({ block: "nearest" })
  }, [activeIndex, open])
  return useRender({
    defaultTagName: "div",
    render,
    state: { empty: ctx.listEmpty },
    props: mergeProps(
      {
        id: idProp ?? ctx.listId,
        role: "listbox",
        tabIndex: -1,
        "aria-multiselectable": ctx.multiple ? "true" : undefined,
        // A function renders every item that survived the filter
        children: typeof children === "function" ? <ComboboxCollection>{children as (item: any, index: number) => ComponentChildren}</ComboboxCollection> : children,
      },
      { ...rest, ref }
    ),
  })
})
namespace ComboboxList {
  export type State = { empty: boolean }
  export type Props = ListProps
}

function ComboboxCollection({ children }: { children: (item: any, index: number) => ComponentChildren }) {
  const ctx = useCombobox("Collection")
  const group = useContext(GroupContext)
  return <>{(group?.items ?? ctx.filteredItems).map(children)}</>
}
namespace ComboboxCollection {
  export type Props = { children: (item: any, index: number) => ComponentChildren }
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type ItemState = { disabled: boolean; selected: boolean; highlighted: boolean }
type ItemProps = Part<"div", ItemState, { value?: any; disabled?: boolean }>

const ComboboxItem = forwardRef<ItemProps>(({ render, value: itemValue = null, disabled: disabledProp = false, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Item")
  const disabled = ctx.disabled || disabledProp
  const index = ctx.indexOf(itemValue)
  const selected = ctx.isSelected(itemValue)
  const highlighted = index !== -1 && ctx.activeIndex === index
  const commit = (event: Event) => {
    const source = ctx.selectionEvent.current ?? event
    ctx.select(itemValue, source)
  }
  const item = useRender({
    defaultTagName: "div",
    render,
    state: { disabled, selected, highlighted },
    props: mergeProps(
      {
        id: index !== -1 ? ctx.itemId(index) : undefined,
        role: "option",
        "aria-selected": String(selected),
        "aria-disabled": disabled ? "true" : undefined,
        // Pressing an item must not move the focus out of the input
        onPointerDownCapture(event: PointerEvent) {
          if (event.isPrimary) ctx.pointerDownItem.current = event.currentTarget as Element
          event.preventDefault()
        },
        onMouseDown: (event: MouseEvent) => event.preventDefault(),
        onClick(event: MouseEvent) {
          if (!disabled) commit(event)
        },
        // Releasing over the highlighted item commits it when the press started elsewhere (on the trigger)
        onMouseUp(event: MouseEvent) {
          const startedHere = ctx.pointerDownItem.current === event.currentTarget
          ctx.pointerDownItem.current = null
          if (!disabled && event.button === 0 && !startedHere && highlighted) commit(event)
        },
        onMouseMove() {
          if (ctx.open && index !== -1 && ctx.activeIndex !== index) ctx.setActive(index)
        },
        onPointerLeave(event: PointerEvent) {
          if (!ctx.open || !ctx.pointerModality.current || event.pointerType === "touch") return
          const next = event.relatedTarget as Element | null
          if (next?.closest("[role=option]")) return
          ctx.setActive(null)
        },
      },
      { ...rest, ref: forwardedRef }
    ),
  })
  return <ItemContext.Provider value={{ selected }}>{item}</ItemContext.Provider>
})
namespace ComboboxItem {
  export type State = ItemState
  export type Props = ItemProps
}

type ItemIndicatorState = { selected: boolean }
type ItemIndicatorProps = Part<"span", ItemIndicatorState>

const ComboboxItemIndicator = forwardRef<ItemIndicatorProps>(({ render, ...rest }, ref) => {
  const item = useContext(ItemContext)
  if (!item) throw new Error("`Combobox.ItemIndicator` must be used within `Combobox.Item`")
  if (!item.selected) return null
  return useRender({ defaultTagName: "span", render, state: { selected: true }, props: mergeProps({ "aria-hidden": "true", children: "✔️" }, { ...rest, ref }) })
})
namespace ComboboxItemIndicator {
  export type State = ItemIndicatorState
  export type Props = ItemIndicatorProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

type GroupProps = Part<"div", {}, { items?: readonly any[] }>

const ComboboxGroup = forwardRef<GroupProps>(({ render, items, ...rest }, ref) => {
  const [labelId, setLabelId] = useState<string | undefined>()
  const value = useMemo(() => ({ labelId, setLabelId, items }), [labelId, items])
  return (
    <GroupContext.Provider value={value}>
      {useRender({ defaultTagName: "div", render, props: mergeProps({ role: "group", "aria-labelledby": labelId }, { ...rest, ref }) })}
    </GroupContext.Provider>
  )
})
namespace ComboboxGroup {
  export type State = {}
  export type Props = GroupProps
}

type GroupLabelProps = Part<"div", {}>

const ComboboxGroupLabel = forwardRef<GroupLabelProps>(({ render, id: idProp, ...rest }, ref) => {
  const group = useContext(GroupContext)
  if (!group) throw new Error("`Combobox.GroupLabel` must be used within `Combobox.Group`")
  const generatedId = useBaseUiId()
  const id = idProp ?? generatedId
  const { setLabelId } = group
  useLayoutEffect(() => {
    setLabelId(id)
    return () => setLabelId(undefined)
  }, [id])
  return useRender({ defaultTagName: "div", render, props: mergeProps({ id, "aria-hidden": "true" }, { ...rest, ref }) })
})
namespace ComboboxGroupLabel {
  export type State = {}
  export type Props = GroupLabelProps
}

type EmptyProps = Part<"div", {}>

const ComboboxEmpty = forwardRef<EmptyProps>(({ render, children, ...rest }, ref) => {
  const ctx = useCombobox("Empty")
  // Always rendered, so a screen reader announces the text that appears when nothing matches
  return useRender({
    defaultTagName: "div",
    render,
    props: mergeProps({ role: "status", "aria-live": "polite", "aria-atomic": "true", children: ctx.listEmpty ? children : null }, { ...rest, ref }),
  })
})
namespace ComboboxEmpty {
  export type State = {}
  export type Props = EmptyProps
}

type SeparatorProps = Part<"div", { orientation: "horizontal" | "vertical" }, { orientation?: "horizontal" | "vertical" }>

const ComboboxSeparator = forwardRef<SeparatorProps>(({ render, orientation = "horizontal", ...rest }, ref) =>
  useRender({ defaultTagName: "div", render, state: { orientation }, props: mergeProps({ role: "presentation" }, { ...rest, ref }) })
)
namespace ComboboxSeparator {
  export type State = { orientation: "horizontal" | "vertical" }
  export type Props = SeparatorProps
}

/* -------------------------------------------------------------------------------------------------------------------------------- */

const INTERACTIVE = 'a[href], button, input, select, textarea, [role="button"], [contenteditable]'

type ChipsProps = Part<"div", {}>

const ComboboxChips = forwardRef<ChipsProps>(({ render, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Chips")
  const registry = useMemo(() => ({ chips: new Set<HTMLElement>() }), [])
  const ref = useComposedRefs(forwardedRef, (element: HTMLElement | null) => void (ctx.chipsRef.current = element))
  return (
    <ChipsContext.Provider value={registry}>
      {useRender({
        defaultTagName: "div",
        render,
        props: mergeProps(
          {
            // Without the toolbar role NVDA leaves focus mode when the arrow keys move inside the container
            role: ctx.hasValue ? "toolbar" : undefined,
            onMouseDown(event: MouseEvent) {
              const target = event.target as Element
              if (target !== event.currentTarget && target.closest(INTERACTIVE)) return
              event.preventDefault()
              if (ctx.disabled) return
              ctx.inputRef.current?.focus()
              if (ctx.openOnInputClick) ctx.setOpen(true, details("input-press", event))
            },
          },
          { ...rest, ref }
        ),
      })}
    </ChipsContext.Provider>
  )
})
namespace ComboboxChips {
  export type State = {}
  export type Props = ChipsProps
}

type ChipProps = Part<"div", { disabled: boolean }>

const ComboboxChip = forwardRef<ChipProps>(({ render, ...rest }, forwardedRef) => {
  const ctx = useCombobox("Chip")
  const chips = useContext(ChipsContext)
  if (!chips) throw new Error("`Combobox.Chip` must be used within `Combobox.Chips`")
  const [element, setElement] = useState<HTMLElement | null>(null)
  useLayoutEffect(() => {
    if (!element) return
    chips.chips.add(element)
    return () => void chips.chips.delete(element)
  }, [element])
  const ref = useComposedRefs(forwardedRef, setElement)
  const index = () => [...chips.chips].sort(byDocumentOrder).indexOf(element!)
  const disabled = ctx.disabled
  const focusChip = (at: number | undefined) => (at == null ? ctx.inputRef.current : [...chips.chips].sort(byDocumentOrder)[at])?.focus()

  return (
    <ChipContext.Provider value={{ index }}>
      {useRender({
        defaultTagName: "div",
        render,
        state: { disabled },
        props: mergeProps(
          {
            tabIndex: -1,
            "aria-disabled": disabled ? "true" : undefined,
            onKeyDown(event: KeyboardEvent) {
              if (disabled) return
              const at = index()
              const count = chips.chips.size
              let next: number | undefined = at
              if (event.key === "ArrowLeft") {
                event.preventDefault()
                next = at > 0 ? at - 1 : undefined
              } else if (event.key === "ArrowRight") {
                event.preventDefault()
                next = at < count - 1 ? at + 1 : undefined
              } else if (event.key === "Backspace" || event.key === "Delete") {
                event.preventDefault()
                event.stopPropagation()
                // The neighbor after the removal: the next chip, or the previous one when the last was removed
                const after = at >= count - 1 ? count - 2 : at
                ctx.setActive(null)
                ctx.removeValueAt(at, details("none", event))
                // The neighbor is found once the chips have rendered without the removed one
                queueMicrotask(() => focusChip(after >= 0 ? after : undefined))
                return
              } else if (event.key === "Enter" || event.key === " ") {
                event.preventDefault()
                event.stopPropagation()
                next = undefined
              } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault()
                event.stopPropagation()
                ctx.setOpen(true, details("list-navigation", event))
                next = undefined
              } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) next = undefined
              focusChip(next)
            },
          },
          { ...rest, ref }
        ),
      })}
    </ChipContext.Provider>
  )
})
namespace ComboboxChip {
  export type State = { disabled: boolean }
  export type Props = ChipProps
}

type ChipRemoveProps = Part<"button", { disabled: boolean }, { disabled?: boolean }>

const ComboboxChipRemove = forwardRef<ChipRemoveProps>(({ render, disabled: disabledProp = false, ...rest }, ref) => {
  const ctx = useCombobox("ChipRemove")
  const chip = useContext(ChipContext)
  if (!chip) throw new Error("`Combobox.ChipRemove` must be used within `Combobox.Chip`")
  const disabled = ctx.disabled || disabledProp
  const remove = (event: Event) => {
    if (disabled) return
    const at = chip.index()
    if (ctx.activeIndex != null && ctx.indexOf(ctx.value[at]) === ctx.activeIndex) ctx.setActive(null)
    ctx.removeValueAt(at, details("chip-remove-press", event))
    ctx.inputRef.current?.focus()
  }
  return useRender({
    defaultTagName: "button",
    render,
    state: { disabled },
    props: mergeProps(
      {
        type: "button",
        tabIndex: -1,
        // Stays focusable when disabled, like Base UI's buttons that explain themselves with aria-disabled
        "aria-disabled": String(disabled),
        onMouseDown: (event: MouseEvent) => event.preventDefault(),
        onClick(event: MouseEvent) {
          remove(event)
          event.stopPropagation()
        },
        onKeyDown(event: KeyboardEvent) {
          if (event.key !== "Enter" && event.key !== " ") return
          event.preventDefault()
          event.stopPropagation()
          remove(event)
        },
      },
      { ...rest, ref }
    ),
  })
})
namespace ComboboxChipRemove {
  export type State = { disabled: boolean }
  export type Props = ChipRemoveProps
}

export {
  ComboboxRoot as Root,
  ComboboxValue as Value,
  ComboboxTrigger as Trigger,
  ComboboxClear as Clear,
  ComboboxInput as Input,
  ComboboxPortal as Portal,
  ComboboxPositioner as Positioner,
  ComboboxPopup as Popup,
  ComboboxList as List,
  ComboboxCollection as Collection,
  ComboboxItem as Item,
  ComboboxItemIndicator as ItemIndicator,
  ComboboxGroup as Group,
  ComboboxGroupLabel as GroupLabel,
  ComboboxEmpty as Empty,
  ComboboxSeparator as Separator,
  ComboboxChips as Chips,
  ComboboxChip as Chip,
  ComboboxChipRemove as ChipRemove,
}
