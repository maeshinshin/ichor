import { useId } from "preact/hooks"
import { Root as CollapsibleRoot, CollapsibleContent, CollapsibleTrigger } from "./collapsible"
import { createCollection } from "./internal/collection"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { mergeStyles } from "./internal/style"
import { useControllableState } from "./internal/use-controllable-state"

const KEYS = ["Home", "End", "ArrowDown", "ArrowUp"]

const Collection = createCollection()

const [AccordionValueProvider, useAccordionValueContext] = createStrictContext<{
  value: string[]
  onItemOpen(value: string): void
  onItemClose(value: string): void
}>("Accordion")
const [AccordionCollapsibleProvider, useAccordionCollapsibleContext] = createStrictContext<{ collapsible: boolean }>("Accordion")
const [AccordionItemProvider, useAccordionItemContext] = createStrictContext<{ open: boolean; disabled?: boolean; triggerId: string }>("AccordionItem")

const getState = (open?: boolean) => (open ? "open" : "closed")

type AccordionSingleProps = PrimitiveProps<"div"> & {
  type: "single"
  value?: string
  defaultValue?: string
  onValueChange?(value: string): void
  collapsible?: boolean
}
type AccordionMultipleProps = PrimitiveProps<"div"> & {
  type: "multiple"
  value?: string[]
  defaultValue?: string[]
  onValueChange?(value: string[]): void
}

// shortcut: vertical only, no group-level `disabled`, `dir` or `orientation`; add them for horizontal accordions
const AccordionImpl = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const getItems = Collection.useCollection()
  return (
    <Collection.Slot>
      <Primitive.div
        data-orientation="vertical"
        {...props}
        ref={ref}
        onKeyDown={composeEventHandlers(props.onKeyDown, (event) => {
          if (!KEYS.includes(event.key)) return
          const triggers = getItems().filter((item) => !(item.ref.current as HTMLButtonElement).disabled)
          const index = triggers.findIndex((item) => item.ref.current === event.target)
          if (index === -1) return

          // Prevents page scroll while navigating
          event.preventDefault()
          const last = triggers.length - 1
          const next =
            event.key === "Home" ? 0 : event.key === "End" ? last : event.key === "ArrowDown" ? (index === last ? 0 : index + 1) : index === 0 ? last : index - 1
          triggers[next]!.ref.current?.focus()
        })}
      />
    </Collection.Slot>
  )
})

const AccordionImplSingle = forwardRef<Omit<AccordionSingleProps, "type">>(
  ({ value: valueProp, defaultValue, onValueChange, collapsible = false, ...props }, ref) => {
    const [value, setValue] = useControllableState({ prop: valueProp, defaultProp: defaultValue ?? "", onChange: onValueChange })
    return (
      <AccordionValueProvider
        value={{
          value: value ? [value] : [],
          onItemOpen: setValue,
          onItemClose: () => collapsible && setValue(""),
        }}
      >
        <AccordionCollapsibleProvider value={{ collapsible }}>
          <AccordionImpl {...props} ref={ref} />
        </AccordionCollapsibleProvider>
      </AccordionValueProvider>
    )
  }
)

const AccordionImplMultiple = forwardRef<Omit<AccordionMultipleProps, "type">>(({ value: valueProp, defaultValue, onValueChange, ...props }, ref) => {
  const [value, setValue] = useControllableState({ prop: valueProp, defaultProp: defaultValue ?? [], onChange: onValueChange })
  return (
    <AccordionValueProvider
      value={{
        value,
        onItemOpen: (itemValue) => setValue((prev) => [...prev, itemValue]),
        onItemClose: (itemValue) => setValue((prev) => prev.filter((v) => v !== itemValue)),
      }}
    >
      <AccordionCollapsibleProvider value={{ collapsible: true }}>
        <AccordionImpl {...props} ref={ref} />
      </AccordionCollapsibleProvider>
    </AccordionValueProvider>
  )
})

const Accordion = forwardRef<AccordionSingleProps | AccordionMultipleProps>(({ type, ...props }, ref) => {
  if (type !== "single" && type !== "multiple") throw new Error("Missing prop `type` expected on `Accordion`")
  return (
    <Collection.Provider>
      {type === "single" ? (
        <AccordionImplSingle {...(props as Omit<AccordionSingleProps, "type">)} ref={ref} />
      ) : (
        <AccordionImplMultiple {...(props as Omit<AccordionMultipleProps, "type">)} ref={ref} />
      )}
    </Collection.Provider>
  )
})

const AccordionItem = forwardRef<PrimitiveWith<"div", { value: string; disabled?: boolean }>>(({ value, disabled, ...props }, ref) => {
  const valueContext = useAccordionValueContext("AccordionItem")
  const open = valueContext.value.includes(value)
  return (
    <AccordionItemProvider value={{ open, disabled, triggerId: useId() }}>
      <CollapsibleRoot
        data-orientation="vertical"
        data-state={getState(open)}
        {...props}
        ref={ref}
        open={open}
        disabled={disabled}
        onOpenChange={(next) => (next ? valueContext.onItemOpen(value) : valueContext.onItemClose(value))}
      />
    </AccordionItemProvider>
  )
})

const AccordionHeader = forwardRef<PrimitiveProps<"h3">>((props, ref) => {
  const item = useAccordionItemContext("AccordionHeader")
  return (
    <Primitive.h3
      data-orientation="vertical"
      data-state={getState(item.open)}
      data-disabled={item.disabled ? "" : undefined}
      {...props}
      ref={ref}
    />
  )
})

const AccordionTrigger = forwardRef<PrimitiveProps<"button">>((props, ref) => {
  const item = useAccordionItemContext("AccordionTrigger")
  const { collapsible } = useAccordionCollapsibleContext("AccordionTrigger")
  return (
    <Collection.ItemSlot>
      <CollapsibleTrigger
        aria-disabled={(item.open && !collapsible) || undefined}
        data-orientation="vertical"
        id={item.triggerId}
        {...props}
        ref={ref}
      />
    </Collection.ItemSlot>
  )
})

const AccordionContent = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const item = useAccordionItemContext("AccordionContent")
  return (
    <CollapsibleContent
      role="region"
      aria-labelledby={item.triggerId}
      data-orientation="vertical"
      {...props}
      ref={ref}
      style={mergeStyles(
        {
          "--radix-accordion-content-height": "var(--radix-collapsible-content-height)",
          "--radix-accordion-content-width": "var(--radix-collapsible-content-width)",
        },
        props.style
      )}
    />
  )
})

export { Accordion as Root, AccordionItem as Item, AccordionHeader as Header, AccordionTrigger as Trigger, AccordionContent as Content }
