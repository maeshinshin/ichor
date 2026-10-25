import type { ComponentChildren, Ref } from "preact"
import { useCallback, useEffect, useRef } from "preact/hooks"
import { Root as Slot } from "../slot"
import { useComposedRefs } from "./compose-refs"
import { createStrictContext } from "./create-context"
import { forwardRef } from "./forward-ref"

const ITEM_DATA_ATTR = "data-radix-collection-item"

// Neither RefObject spelling fits both: Preact 11 rejects RefObject<HTMLElement> (no null), Preact 10 rejects RefObject<HTMLElement | null>
// where a Ref<HTMLElement> is expected
type ElementRef = { current: HTMLElement | null }

export function createCollection<ItemData extends object = {}>() {
  type Item = ItemData & { ref: ElementRef }
  type ContextValue = { collectionRef: ElementRef; itemMap: Map<ElementRef, Item> }

  const [ContextProvider, useContextValue] = createStrictContext<ContextValue>("Collection.Provider")

  function Provider({ children }: { children?: ComponentChildren }) {
    const collectionRef = useRef<HTMLElement>(null)
    const itemMap = useRef(new Map<ElementRef, Item>())
    const value = useRef({ collectionRef, itemMap: itemMap.current }).current
    return <ContextProvider value={value}>{children}</ContextProvider>
  }

  const CollectionSlot = forwardRef<{ children?: ComponentChildren; ref?: Ref<HTMLElement> }>(
    ({ children }, forwardedRef) => {
      const context = useContextValue("Collection.Slot")
      const composedRefs = useComposedRefs(forwardedRef, context.collectionRef)
      return <Slot ref={composedRefs}>{children}</Slot>
    }
  )

  const ItemSlot = forwardRef<ItemData & { children?: ComponentChildren; ref?: Ref<HTMLElement> }>(
    ({ children, ...itemData }, forwardedRef) => {
      const ref = useRef<HTMLElement>(null)
      const composedRefs = useComposedRefs(forwardedRef, ref)
      const context = useContextValue("Collection.ItemSlot")

      useEffect(() => {
        context.itemMap.set(ref, { ref, ...(itemData as unknown as ItemData) })
        return () => void context.itemMap.delete(ref)
      })

      return (
        <Slot {...{ [ITEM_DATA_ATTR]: "" }} ref={composedRefs}>
          {children}
        </Slot>
      )
    }
  )

  function useCollection() {
    const context = useContextValue("useCollection")
    return useCallback(() => {
      const collectionNode = context.collectionRef.current
      if (!collectionNode) return []
      const orderedNodes = Array.from(collectionNode.querySelectorAll(`[${ITEM_DATA_ATTR}]`))
      return Array.from(context.itemMap.values()).sort(
        (a, b) => orderedNodes.indexOf(a.ref.current!) - orderedNodes.indexOf(b.ref.current!)
      )
    }, [context])
  }

  return { Provider, Slot: CollectionSlot, ItemSlot, useCollection }
}
