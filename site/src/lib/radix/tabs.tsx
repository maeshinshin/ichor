import { useEffect, useId, useRef } from "preact/hooks"
import { useDirection, type Direction } from "./direction"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import * as RovingFocusGroup from "./internal/roving-focus"
import { mergeStyles } from "./internal/style"
import { useControllableState } from "./internal/use-controllable-state"

type Orientation = "horizontal" | "vertical"

const [TabsProvider, useTabsContext] = createStrictContext<{
  baseId: string
  value: string
  onValueChange(value: string): void
  orientation: Orientation
  dir: Direction
}>("Tabs")

const makeTriggerId = (baseId: string, value: string) => `${baseId}-trigger-${value}`
const makeContentId = (baseId: string, value: string) => `${baseId}-content-${value}`

// shortcut: tabs always activate on focus (no `activationMode="manual"`) and the list always loops; add the props if a caller needs them
const Tabs = forwardRef<
  PrimitiveWith<
    "div",
    { value?: string; defaultValue?: string; onValueChange?(value: string): void; orientation?: Orientation; dir?: Direction }
  >
>(({ value: valueProp, onValueChange, defaultValue, orientation = "horizontal", dir, ...tabsProps }, ref) => {
  const direction = useDirection(dir)
  const [value, setValue] = useControllableState({ prop: valueProp, onChange: onValueChange, defaultProp: defaultValue ?? "" })
  return (
    <TabsProvider value={{ baseId: useId(), value, onValueChange: setValue, orientation, dir: direction }}>
      <Primitive.div dir={direction} data-orientation={orientation} {...tabsProps} ref={ref} />
    </TabsProvider>
  )
})

const TabsList = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const context = useTabsContext("TabsList")
  return (
    <RovingFocusGroup.Root asChild orientation={context.orientation} dir={context.dir} loop>
      <Primitive.div role="tablist" aria-orientation={context.orientation} {...props} ref={ref} />
    </RovingFocusGroup.Root>
  )
})

const TabsTrigger = forwardRef<PrimitiveWith<"button", { value: string; disabled?: boolean }>>(
  ({ value, disabled = false, ...triggerProps }, ref) => {
    const context = useTabsContext("TabsTrigger")
    const isSelected = value === context.value
    return (
      <RovingFocusGroup.Item asChild focusable={!disabled} active={isSelected}>
        <Primitive.button
          type="button"
          role="tab"
          aria-selected={isSelected}
          aria-controls={makeContentId(context.baseId, value)}
          data-state={isSelected ? "active" : "inactive"}
          data-disabled={disabled ? "" : undefined}
          disabled={disabled}
          id={makeTriggerId(context.baseId, value)}
          {...triggerProps}
          ref={ref}
          onMouseDown={composeEventHandlers(triggerProps.onMouseDown, (event) => {
            // Only the main button without ctrl selects the tab; ctrl+click is the macOS context menu
            if (!disabled && event.button === 0 && !event.ctrlKey) {
              // Safari does not focus a button on click
              event.currentTarget.focus()
              context.onValueChange(value)
            } else event.preventDefault()
          })}
          onKeyDown={composeEventHandlers(triggerProps.onKeyDown, (event) => {
            if (event.key === " " || event.key === "Enter") context.onValueChange(value)
          })}
          onFocus={composeEventHandlers(triggerProps.onFocus, () => {
            if (!isSelected && !disabled) context.onValueChange(value)
          })}
        />
      </RovingFocusGroup.Item>
    )
  }
)

const TabsContent = forwardRef<PrimitiveWith<"div", { value: string }>>(({ value, children, ...contentProps }, ref) => {
  const context = useTabsContext("TabsContent")
  const isSelected = value === context.value
  const isMountAnimationPrevented = useRef(isSelected)

  useEffect(() => {
    const frame = requestAnimationFrame(() => (isMountAnimationPrevented.current = false))
    return () => cancelAnimationFrame(frame)
  }, [])

  return (
    <Presence present={isSelected}>
      {({ present }) => (
        <Primitive.div
          data-state={isSelected ? "active" : "inactive"}
          data-orientation={context.orientation}
          role="tabpanel"
          aria-labelledby={makeTriggerId(context.baseId, value)}
          hidden={!present}
          id={makeContentId(context.baseId, value)}
          tabIndex={0}
          {...contentProps}
          ref={ref}
          style={mergeStyles(contentProps.style, { animationDuration: isMountAnimationPrevented.current ? "0s" : undefined })}
        >
          {present && children}
        </Primitive.div>
      )}
    </Presence>
  )
})

export { Tabs as Root, TabsList as List, TabsTrigger as Trigger, TabsContent as Content }
