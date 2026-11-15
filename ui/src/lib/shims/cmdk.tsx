import { createContext, type ComponentChildren, type CSSProperties, type HTMLAttributes, type InputHTMLAttributes, type Ref, type RefObject } from "preact"
import { useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks"
import { composeEventHandlers } from "../radix/internal/compose-event-handlers"
import { useComposedRefs } from "../radix/internal/compose-refs"
import { createStrictContext } from "../radix/internal/create-context"
import { forwardRef } from "../radix/internal/forward-ref"

// cmdk 1.1.1 on Preact hooks: the same cmdk-* attributes, aria wiring, command-score and keys.
// shortcut: no `asChild`, `Command.Dialog`, `Command.Loading`, `useCommandState` or `defaultFilter` (the shadcn wrapper uses none and builds its
// dialog from the Radix dialog shim); port the one a caller needs from cmdk.

type DivProps = Omit<HTMLAttributes<HTMLDivElement>, "ref"> & { ref?: Ref<any> }
type With<Own> = Omit<DivProps, keyof Own> & Own

type CommandFilter = (value: string, search: string, keywords?: string[]) => number
type CommandProps = With<{
  /** Accessible label for the menu, not shown visibly. */
  label?: string
  /** `false` turns off automatic filtering and sorting. */
  shouldFilter?: boolean
  /** Returns 0 to hide an item, up to 1 for the best match. Defaults to cmdk's command-score. */
  filter?: CommandFilter
  defaultValue?: string
  value?: string
  onValueChange?(value: string): void
  /** Wrap around at both ends when moving with the arrow keys. */
  loop?: boolean
  disablePointerSelection?: boolean
  /** ctrl+n/j/p/k move the selection. Defaults to true. */
  vimBindings?: boolean
}>
type ItemProps = With<{
  disabled?: boolean
  onSelect?(value: string): void
  /** Defaults to the trimmed text content. Provide a stable one when the text changes between renders. */
  value?: string
  keywords?: string[]
  forceMount?: boolean
}>
type GroupProps = With<{ heading?: ComponentChildren; value?: string; forceMount?: boolean }>
type SeparatorProps = With<{ alwaysRender?: boolean }>
type ListProps = With<{ label?: string }>
type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "type" | "ref"> & {
  ref?: Ref<any>
  value?: string
  onValueChange?(search: string): void
}

type State = {
  search: string
  value: string | undefined
  selectedItemId?: string
  filtered: { count: number; items: Map<string, number>; groups: Set<string> }
}
type Store = {
  subscribe(callback: () => void): () => void
  snapshot(): State
  setState<K extends keyof State>(key: K, value: State[K], skipScroll?: boolean): void
  emit(): void
  /** Subscribers told to re-render that have not committed yet. */
  dirty: Set<object>
  settled(subscriber: object): void
}
type Context = {
  store: Store
  value(id: string, value: string, keywords?: string[]): void
  item(id: string, groupId?: string): () => void
  selectedItem(id: string): () => void
  group(id: string): () => void
  filter(): boolean | undefined
  getDisablePointerSelection(): boolean | undefined
  listId: string
  labelId: string
  inputId: string
  listInnerRef: RefObject<HTMLDivElement | null>
}

const GROUP_SELECTOR = `[cmdk-group=""]`
const GROUP_ITEMS_SELECTOR = `[cmdk-group-items=""]`
const GROUP_HEADING_SELECTOR = `[cmdk-group-heading=""]`
const ITEM_SELECTOR = `[cmdk-item=""]`
const VALID_ITEM_SELECTOR = `${ITEM_SELECTOR}:not([aria-disabled="true"])`
const SELECT_EVENT = "cmdk-item-select"
const VALUE_ATTR = "data-value"

const [CommandProvider, useCommand] = createStrictContext<Context>("Command")
const GroupContext = createContext<{ id: string; forceMount?: boolean } | undefined>(undefined)
const useLazy = <T,>(init: () => T) => useState(init)[0]

const srOnlyStyles: CSSProperties = {
  position: "absolute",
  width: "1px",
  height: "1px",
  padding: "0",
  margin: "-1px",
  overflow: "hidden",
  clip: "rect(0, 0, 0, 0)",
  whiteSpace: "nowrap",
  borderWidth: "0",
}

const CommandBase = forwardRef<CommandProps>((props, forwardedRef) => {
  const state = useLazy<State>(() => ({
    search: "",
    value: props.value ?? props.defaultValue ?? "",
    selectedItemId: undefined,
    // count of visible items; item id to score; groups with at least one visible item
    filtered: { count: 0, items: new Map(), groups: new Set() },
  }))
  const allItems = useLazy(() => new Set<string>())
  const allGroups = useLazy(() => new Map<string, Set<string>>())
  const ids = useLazy(() => new Map<string, { value: string; keywords?: string[] }>())
  const listeners = useLazy(() => new Set<() => void>())
  const propsRef = useRef(props)
  propsRef.current = props
  const { label, children, value, defaultValue, onValueChange, filter, shouldFilter, loop, disablePointerSelection, vimBindings = true, ...etc } = props
  const listId = useId()
  const labelId = useId()
  const inputId = useId()
  const listInnerRef = useRef<HTMLDivElement | null>(null)

  // Work that must see the DOM after the items re-rendered, keyed so a burst of items runs each step once. React's layout effect does
  // that on its own (useSyncExternalStore re-renders the items in the same pass); here it waits until no subscriber is left stale.
  const scheduled = useLazy(() => new Map<number, () => void>())
  const dirty = useLazy(() => new Set<object>())
  const schedule = (id: number, callback: () => void) => {
    if (!scheduled.size) queueMicrotask(() => dirty.size || flush())
    scheduled.set(id, callback)
  }
  function flush() {
    const callbacks = [...scheduled.values()]
    scheduled.clear()
    // The list is gone when the menu unmounted before this ran
    if (listInnerRef.current) callbacks.forEach((callback) => callback())
  }

  // Controlled `value`
  useLayoutEffect(() => {
    if (value !== undefined) {
      state.value = value.trim()
      store.emit()
    }
  }, [value])

  useLayoutEffect(() => {
    schedule(6, scrollSelectedIntoView)
  }, [])

  const store: Store = useMemo(
    () => ({
      subscribe: (callback) => {
        listeners.add(callback)
        return () => void listeners.delete(callback)
      },
      snapshot: () => state,
      setState: (key, value, skipScroll) => {
        if (Object.is(state[key], value)) return
        state[key] = value

        if (key === "search") {
          // Filter synchronously before emitting back to children
          filterItems()
          sort()
        } else if (key === "value") {
          // Keep focus on the input (or list) so the active descendant is announced
          const active = document.activeElement
          if (active?.hasAttribute("cmdk-input") || active?.hasAttribute("cmdk-root")) {
            const input = document.getElementById(inputId)
            if (input) input.focus()
            else document.getElementById(listId)?.focus()
          }
        }

        // cmdk reports value changes only when controlled; the menu follows its own state until the prop catches up
        if (key === "value" && propsRef.current.value !== undefined) propsRef.current.onValueChange?.((value ?? "") as string)
        store.emit()

        if (key === "search") schedule(1, selectFirstItem)
        else if (key === "value" && !skipScroll) schedule(5, scrollSelectedIntoView)
      },
      emit: () => listeners.forEach((listener) => listener()),
      dirty,
      settled: (subscriber) => {
        if (dirty.delete(subscriber) && !dirty.size) flush()
      },
    }),
    []
  )

  const context: Context = useMemo(
    () => ({
      store,
      // Keep id to {value, keywords} up to date
      value: (id, value, keywords) => {
        if (value !== ids.get(id)?.value) {
          ids.set(id, { value, keywords })
          state.filtered.items.set(id, score(value, keywords))
          schedule(2, () => {
            sort()
            store.emit()
          })
        }
      },
      // Track item lifecycle (mount, unmount)
      item: (id, groupId) => {
        allItems.add(id)
        if (groupId) {
          if (!allGroups.has(groupId)) allGroups.set(groupId, new Set([id]))
          else allGroups.get(groupId)!.add(id)
        }

        // Batched: several items can mount in one pass
        schedule(3, () => {
          filterItems()
          sort()
          // Initial mount: select the first item
          if (!state.value) selectFirstItem()
          store.emit()
        })

        return () => {
          ids.delete(id)
          allItems.delete(id)
          state.filtered.items.delete(id)
          const selectedItem = getSelectedItem()

          schedule(4, () => {
            filterItems()
            // The removed item was the selected one: move to the first
            if (selectedItem?.getAttribute("id") === id) selectFirstItem()
            store.emit()
          })
        }
      },
      group: (id) => {
        if (!allGroups.has(id)) allGroups.set(id, new Set())
        return () => {
          ids.delete(id)
          allGroups.delete(id)
        }
      },
      // The selected item reports its id for aria-activedescendant once it has rendered selected
      selectedItem: (id) => {
        state.selectedItemId = id
        store.emit()
        return () => {
          if (state.selectedItemId !== id) return
          state.selectedItemId = undefined
          store.emit()
        }
      },
      filter: () => propsRef.current.shouldFilter,
      getDisablePointerSelection: () => propsRef.current.disablePointerSelection,
      listId,
      inputId,
      labelId,
      listInnerRef,
    }),
    []
  )

  function score(value: string, keywords?: string[]) {
    const filter = propsRef.current.filter ?? commandScore
    return value ? filter(value, state.search, keywords) : 0
  }

  // Reorders the item elements by score within their container. cmdk also tries to reorder the groups by their best item, but looks them
  // up by id while they carry their heading as data-value, so it never moves one; groups stay in source order here too.
  function sort() {
    if (!state.search || propsRef.current.shouldFilter === false) return
    const scores = state.filtered.items
    const listElement = listInnerRef.current!
    getValidItems()
      .sort((a, b) => (scores.get(b.id) ?? 0) - (scores.get(a.id) ?? 0))
      .forEach((item) => {
        const group = item.closest(GROUP_ITEMS_SELECTOR)
        const container = group ?? listElement
        container.appendChild(item.parentElement === container ? item : item.closest(`${GROUP_ITEMS_SELECTOR} > *`)!)
      })
  }

  function selectFirstItem() {
    store.setState("value", getValidItems()[0]?.getAttribute(VALUE_ATTR) || undefined)
  }

  function filterItems() {
    if (!state.search || propsRef.current.shouldFilter === false) {
      // Each item shows itself because the search is empty
      state.filtered.count = allItems.size
      return
    }

    state.filtered.groups = new Set()
    let itemCount = 0
    for (const id of allItems) {
      const rank = score(ids.get(id)?.value ?? "", ids.get(id)?.keywords ?? [])
      state.filtered.items.set(id, rank)
      if (rank > 0) itemCount++
    }
    // A group stays when at least one of its items does
    for (const [groupId, group] of allGroups) {
      for (const itemId of group) {
        if ((state.filtered.items.get(itemId) ?? 0) > 0) {
          state.filtered.groups.add(groupId)
          break
        }
      }
    }
    state.filtered.count = itemCount
  }

  function scrollSelectedIntoView() {
    const item = getSelectedItem()
    if (!item) return
    // First item of a group: keep the heading in view too
    if (item.parentElement?.firstChild === item) item.closest(GROUP_SELECTOR)?.querySelector(GROUP_HEADING_SELECTOR)?.scrollIntoView({ block: "nearest" })
    item.scrollIntoView({ block: "nearest" })
  }

  const getSelectedItem = () => listInnerRef.current?.querySelector(`${ITEM_SELECTOR}[aria-selected="true"]`) ?? null
  const getValidItems = () => Array.from(listInnerRef.current?.querySelectorAll<HTMLElement>(VALID_ITEM_SELECTOR) ?? [])

  function updateSelectedToIndex(index: number) {
    const item = getValidItems()[index]
    if (item) store.setState("value", item.getAttribute(VALUE_ATTR)!)
  }

  function updateSelectedByItem(change: 1 | -1) {
    const items = getValidItems()
    const index = items.findIndex((item) => item === getSelectedItem())
    let next = items[index + change]
    if (propsRef.current.loop) next = index + change < 0 ? items[items.length - 1] : index + change === items.length ? items[0] : items[index + change]
    if (next) store.setState("value", next.getAttribute(VALUE_ATTR)!)
  }

  function updateSelectedByGroup(change: 1 | -1) {
    let group = getSelectedItem()?.closest(GROUP_SELECTOR)
    let item: Element | null | undefined
    while (group && !item) {
      group = change > 0 ? findSibling(group, GROUP_SELECTOR, "nextElementSibling") : findSibling(group, GROUP_SELECTOR, "previousElementSibling")
      item = group?.querySelector(VALID_ITEM_SELECTOR)
    }
    if (item) store.setState("value", item.getAttribute(VALUE_ATTR)!)
    else updateSelectedByItem(change)
  }

  const last = () => updateSelectedToIndex(getValidItems().length - 1)

  const next = (event: KeyboardEvent) => {
    event.preventDefault()
    if (event.metaKey) last()
    else if (event.altKey) updateSelectedByGroup(1)
    else updateSelectedByItem(1)
  }

  const prev = (event: KeyboardEvent) => {
    event.preventDefault()
    if (event.metaKey) updateSelectedToIndex(0)
    else if (event.altKey) updateSelectedByGroup(-1)
    else updateSelectedByItem(-1)
  }

  return (
    <div
      ref={forwardedRef}
      tabIndex={-1}
      {...etc}
      cmdk-root=""
      onKeyDown={(event) => {
        etc.onKeyDown?.(event)

        // Ignore keys that belong to an IME composition (keyCode 229 on legacy browsers)
        if (event.defaultPrevented || event.isComposing || event.keyCode === 229) return

        switch (event.key) {
          case "n":
          case "j":
            if (vimBindings && event.ctrlKey) next(event)
            break
          case "ArrowDown":
            next(event)
            break
          case "p":
          case "k":
            if (vimBindings && event.ctrlKey) prev(event)
            break
          case "ArrowUp":
            prev(event)
            break
          case "Home":
            event.preventDefault()
            updateSelectedToIndex(0)
            break
          case "End":
            event.preventDefault()
            last()
            break
          case "Enter": {
            event.preventDefault()
            getSelectedItem()?.dispatchEvent(new Event(SELECT_EVENT))
          }
        }
      }}
    >
      <label cmdk-label="" htmlFor={inputId} id={labelId} style={srOnlyStyles}>
        {label}
      </label>
      <CommandProvider value={context}>{children}</CommandProvider>
    </div>
  )
})

const Item = forwardRef<ItemProps>((props, forwardedRef) => {
  const id = useId()
  const ref = useRef<HTMLDivElement | null>(null)
  const groupContext = useContext(GroupContext)
  const context = useCommand("Command.Item")
  const propsRef = useRef(props)
  propsRef.current = props
  const forceMount = props.forceMount ?? groupContext?.forceMount

  useLayoutEffect(() => {
    if (!forceMount) return context.item(id, groupContext?.id)
  }, [forceMount])

  const value = useValue(context, id, ref, [props.value, props.children, ref], props.keywords)
  const selected = useCmdk(context, (state) => Boolean(state.value) && state.value === value.current)

  useLayoutEffect(() => {
    if (selected) return context.selectedItem(id)
  }, [selected])
  const render = useCmdk(context, (state) => (forceMount || context.filter() === false || !state.search ? true : (state.filtered.items.get(id) ?? 0) > 0))

  // The root dispatches this event on the selected item when Enter is pressed
  useEffect(() => {
    const element = ref.current
    if (!element || props.disabled) return
    element.addEventListener(SELECT_EVENT, onSelect)
    return () => element.removeEventListener(SELECT_EVENT, onSelect)
  }, [render, props.onSelect, props.disabled])

  const composedRef = useComposedRefs(ref, forwardedRef)

  function onSelect() {
    select()
    propsRef.current.onSelect?.(value.current!)
  }

  function select() {
    context.store.setState("value", value.current, true)
  }

  if (!render) return null

  const { disabled, value: _value, onSelect: _onSelect, forceMount: _forceMount, keywords: _keywords, ...etc } = props

  return (
    <div
      ref={composedRef}
      {...etc}
      id={id}
      cmdk-item=""
      role="option"
      aria-disabled={Boolean(disabled)}
      aria-selected={Boolean(selected)}
      data-disabled={Boolean(disabled)}
      data-selected={Boolean(selected)}
      onPointerMove={disabled || context.getDisablePointerSelection() ? undefined : select}
      onClick={disabled ? undefined : onSelect}
    >
      {props.children}
    </div>
  )
})

const Group = forwardRef<GroupProps>((props, forwardedRef) => {
  const { heading, children, forceMount, ...etc } = props
  const id = useId()
  const ref = useRef<HTMLDivElement | null>(null)
  const headingRef = useRef<HTMLDivElement | null>(null)
  const headingId = useId()
  const context = useCommand("Command.Group")
  const render = useCmdk(context, (state) => (forceMount || context.filter() === false || !state.search ? true : state.filtered.groups.has(id)))
  const composedRef = useComposedRefs(ref, forwardedRef)

  useLayoutEffect(() => context.group(id), [])
  useValue(context, id, ref, [props.value, props.heading, headingRef])

  const groupContext = useMemo(() => ({ id, forceMount }), [forceMount])

  return (
    <div ref={composedRef} {...etc} cmdk-group="" role="presentation" hidden={render ? undefined : true}>
      {heading && (
        <div ref={headingRef} cmdk-group-heading="" aria-hidden id={headingId}>
          {heading}
        </div>
      )}
      <div cmdk-group-items="" role="group" aria-labelledby={heading ? headingId : undefined}>
        <GroupContext.Provider value={groupContext}>{children}</GroupContext.Provider>
      </div>
    </div>
  )
})

const Separator = forwardRef<SeparatorProps>(({ alwaysRender, ...etc }, forwardedRef) => {
  const render = useCmdk(useCommand("Command.Separator"), (state) => !state.search)
  if (!alwaysRender && !render) return null
  return <div ref={forwardedRef} {...etc} cmdk-separator="" role="separator" />
})

const Input = forwardRef<InputProps>(({ onValueChange, onInput, ...etc }, forwardedRef) => {
  const isControlled = etc.value != null
  const context = useCommand("Command.Input")
  const search = useCmdk(context, (state) => state.search)
  const selectedItemId = useCmdk(context, (state) => state.selectedItemId)

  useEffect(() => {
    if (etc.value != null) context.store.setState("search", etc.value)
  }, [etc.value])

  return (
    <input
      ref={forwardedRef}
      {...etc}
      cmdk-input=""
      autoComplete="off"
      autoCorrect="off"
      spellcheck={false}
      aria-autocomplete="list"
      role="combobox"
      aria-expanded={true}
      aria-controls={context.listId}
      aria-labelledby={context.labelId}
      aria-activedescendant={selectedItemId}
      id={context.inputId}
      type="text"
      value={isControlled ? etc.value : search}
      // Preact's onChange is the native one, which only fires on commit; onInput is React's onChange
      onInput={composeEventHandlers(
        onInput,
        (event) => {
          const text = event.currentTarget.value
          if (!isControlled) context.store.setState("search", text)
          onValueChange?.(text)
        },
        { checkForDefaultPrevented: false }
      )}
    />
  )
})

const List = forwardRef<ListProps>(({ children, label = "Suggestions", ...etc }, forwardedRef) => {
  const ref = useRef<HTMLDivElement | null>(null)
  const height = useRef<HTMLDivElement | null>(null)
  const context = useCommand("Command.List")
  const selectedItemId = useCmdk(context, (state) => state.selectedItemId)
  const composedRef = useComposedRefs(ref, forwardedRef)
  const sizerRef = useComposedRefs(height, context.listInnerRef)

  // Publishes the content height as --cmdk-list-height so the list can animate its size
  useEffect(() => {
    const wrapper = ref.current
    const element = height.current
    if (!wrapper || !element) return
    let frame = 0
    const observer = new ResizeObserver(() => {
      frame = requestAnimationFrame(() => wrapper.style.setProperty("--cmdk-list-height", element.offsetHeight.toFixed(1) + "px"))
    })
    observer.observe(element)
    return () => {
      cancelAnimationFrame(frame)
      observer.unobserve(element)
    }
  }, [])

  return (
    <div ref={composedRef} {...etc} cmdk-list="" role="listbox" tabIndex={-1} aria-activedescendant={selectedItemId} aria-label={label} id={context.listId}>
      <div ref={sizerRef} cmdk-list-sizer="">
        {children}
      </div>
    </div>
  )
})

const Empty = forwardRef<DivProps>((props, forwardedRef) => {
  const render = useCmdk(useCommand("Command.Empty"), (state) => state.filtered.count === 0)
  if (!render) return null
  return <div ref={forwardedRef} {...props} cmdk-empty="" role="presentation" />
})

export const Command = Object.assign(CommandBase, { List, Item, Input, Group, Separator, Empty })

// cmdk reads the store with useSyncExternalStore, which Preact does not have
function useCmdk<T>({ store }: Context, selector: (state: State) => T): T {
  const [, update] = useState({})
  const subscriber = useLazy(() => ({}))
  const selectorRef = useRef(selector)
  selectorRef.current = selector
  const snapshot = selector(store.snapshot())
  const rendered = useRef(snapshot)
  rendered.current = snapshot

  // This render has the latest snapshot. Declared before the subscription so a change it finds right away stays marked.
  useLayoutEffect(() => store.settled(subscriber))
  useLayoutEffect(() => {
    const check = () => {
      if (store.dirty.has(subscriber) || Object.is(selectorRef.current(store.snapshot()), rendered.current)) return
      store.dirty.add(subscriber)
      update({})
    }
    check()
    const unsubscribe = store.subscribe(check)
    return () => {
      unsubscribe()
      store.settled(subscriber)
    }
  }, [store])

  return snapshot
}

// The item (or group) value: the given string, else the element's text content, kept in the context for filtering
function useValue(context: Context, id: string, ref: RefObject<HTMLElement | null>, deps: unknown[], keywords: string[] = []) {
  const valueRef = useRef<string | undefined>(undefined)

  useLayoutEffect(() => {
    const value = (() => {
      for (const part of deps) {
        if (typeof part === "string") return part.trim()
        if (part && typeof part === "object" && "current" in part) {
          return (part as RefObject<HTMLElement | null>).current ? (part as RefObject<HTMLElement>).current!.textContent?.trim() : valueRef.current
        }
      }
    })()
    if (value === undefined) return

    context.value(id, value, keywords.map((keyword) => keyword.trim()))
    ref.current?.setAttribute(VALUE_ATTR, value)
    valueRef.current = value
  })

  return valueRef
}

function findSibling(element: Element, selector: string, direction: "nextElementSibling" | "previousElementSibling") {
  let sibling = element[direction]
  while (sibling) {
    if (sibling.matches(selector)) return sibling
    sibling = sibling[direction]
  }
  return null
}

// cmdk 1.1.1's command-score, unchanged: a run of matching characters scores 1, a match at a word start scores less, a gap or a
// transposition less again.
const SCORE_CONTINUE_MATCH = 1
const SCORE_SPACE_WORD_JUMP = 0.9
const SCORE_NON_SPACE_WORD_JUMP = 0.8
const SCORE_CHARACTER_JUMP = 0.17
const SCORE_TRANSPOSITION = 0.1
const PENALTY_SKIPPED = 0.999
const PENALTY_CASE_MISMATCH = 0.9999
const PENALTY_NOT_COMPLETE = 0.99

const IS_GAP_REGEXP = /[\\\/_+.#"@\[\(\{&]/
const COUNT_GAPS_REGEXP = /[\\\/_+.#"@\[\(\{&]/g
const IS_SPACE_REGEXP = /[\s-]/
const COUNT_SPACE_REGEXP = /[\s-]/g

function commandScoreInner(
  string: string,
  abbreviation: string,
  lowerString: string,
  lowerAbbreviation: string,
  stringIndex: number,
  abbreviationIndex: number,
  memoizedResults: Record<string, number>
): number {
  if (abbreviationIndex === abbreviation.length) return stringIndex === string.length ? SCORE_CONTINUE_MATCH : PENALTY_NOT_COMPLETE

  const memoizeKey = `${stringIndex},${abbreviationIndex}`
  if (memoizedResults[memoizeKey] !== undefined) return memoizedResults[memoizeKey]

  const abbreviationChar = lowerAbbreviation.charAt(abbreviationIndex)
  let index = lowerString.indexOf(abbreviationChar, stringIndex)
  let highScore = 0

  while (index >= 0) {
    let score = commandScoreInner(string, abbreviation, lowerString, lowerAbbreviation, index + 1, abbreviationIndex + 1, memoizedResults)
    if (score > highScore) {
      if (index === stringIndex) {
        score *= SCORE_CONTINUE_MATCH
      } else if (IS_GAP_REGEXP.test(string.charAt(index - 1))) {
        score *= SCORE_NON_SPACE_WORD_JUMP
        const wordBreaks = string.slice(stringIndex, index - 1).match(COUNT_GAPS_REGEXP)
        if (wordBreaks && stringIndex > 0) score *= Math.pow(PENALTY_SKIPPED, wordBreaks.length)
      } else if (IS_SPACE_REGEXP.test(string.charAt(index - 1))) {
        score *= SCORE_SPACE_WORD_JUMP
        const spaceBreaks = string.slice(stringIndex, index - 1).match(COUNT_SPACE_REGEXP)
        if (spaceBreaks && stringIndex > 0) score *= Math.pow(PENALTY_SKIPPED, spaceBreaks.length)
      } else {
        score *= SCORE_CHARACTER_JUMP
        if (stringIndex > 0) score *= Math.pow(PENALTY_SKIPPED, index - stringIndex)
      }

      if (string.charAt(index) !== abbreviation.charAt(abbreviationIndex)) score *= PENALTY_CASE_MISMATCH
    }

    if (
      (score < SCORE_TRANSPOSITION && lowerString.charAt(index - 1) === lowerAbbreviation.charAt(abbreviationIndex + 1)) ||
      (lowerAbbreviation.charAt(abbreviationIndex + 1) === lowerAbbreviation.charAt(abbreviationIndex) &&
        lowerString.charAt(index - 1) !== lowerAbbreviation.charAt(abbreviationIndex))
    ) {
      const transposedScore = commandScoreInner(string, abbreviation, lowerString, lowerAbbreviation, index + 1, abbreviationIndex + 2, memoizedResults)
      if (transposedScore * SCORE_TRANSPOSITION > score) score = transposedScore * SCORE_TRANSPOSITION
    }

    if (score > highScore) highScore = score
    index = lowerString.indexOf(abbreviationChar, index + 1)
  }

  memoizedResults[memoizeKey] = highScore
  return highScore
}

// All kinds of whitespace and hyphens match each other
const formatInput = (string: string) => string.toLowerCase().replace(COUNT_SPACE_REGEXP, " ")

function commandScore(string: string, abbreviation: string, aliases?: string[]) {
  string = aliases && aliases.length > 0 ? `${string} ${aliases.join(" ")}` : string
  return commandScoreInner(string, abbreviation, formatInput(string), formatInput(abbreviation), 0, 0, {})
}
