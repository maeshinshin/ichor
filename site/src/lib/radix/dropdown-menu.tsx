import type { ComponentChildren, RefObject } from "preact"
import { useId, useRef } from "preact/hooks"
import type { Direction } from "./direction"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import * as MenuPrimitive from "./internal/menu"
import { Primitive, type PrimitiveWith } from "./internal/primitive"
import { useControllableState } from "./internal/use-controllable-state"

const [DropdownMenuProvider, useDropdownMenuContext] = createStrictContext<{
  triggerId: string
  triggerRef: RefObject<HTMLButtonElement | null>
  contentId: string
  open: boolean
  onOpenChange(open: boolean): void
  onOpenToggle(): void
  modal: boolean
}>("DropdownMenu")

function DropdownMenu({
  open: openProp,
  defaultOpen,
  onOpenChange,
  modal = true,
  dir,
  children,
}: {
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?(open: boolean): void
  modal?: boolean
  dir?: Direction
  children?: ComponentChildren
}) {
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const [open, setOpen] = useControllableState({ prop: openProp, defaultProp: defaultOpen ?? false, onChange: onOpenChange })
  return (
    <DropdownMenuProvider
      value={{
        triggerId: useId(),
        triggerRef,
        contentId: useId(),
        open,
        onOpenChange: setOpen,
        onOpenToggle: () => setOpen((prev) => !prev),
        modal,
      }}
    >
      <MenuPrimitive.Root name="dropdown-menu" open={open} onOpenChange={setOpen} dir={dir} modal={modal}>
        {children}
      </MenuPrimitive.Root>
    </DropdownMenuProvider>
  )
}

const DropdownMenuTrigger = forwardRef<PrimitiveWith<"button", { disabled?: boolean }>>(({ disabled = false, ...triggerProps }, forwardedRef) => {
  const context = useDropdownMenuContext("DropdownMenuTrigger")
  const ref = useComposedRefs(forwardedRef, context.triggerRef)
  return (
    <MenuPrimitive.Anchor asChild>
      <Primitive.button
        type="button"
        id={context.triggerId}
        aria-haspopup="menu"
        aria-expanded={context.open}
        aria-controls={context.open ? context.contentId : undefined}
        data-state={context.open ? "open" : "closed"}
        data-disabled={disabled ? "" : undefined}
        disabled={disabled}
        {...triggerProps}
        ref={ref}
        onPointerDown={composeEventHandlers(triggerProps.onPointerDown, (event) => {
          // Only the left button, and not ctrl+click, which is a right click on macOS
          if (!disabled && event.button === 0 && event.ctrlKey === false) {
            context.onOpenToggle()
            // Opening must not focus the trigger, so the content can take focus without competition
            if (!context.open) event.preventDefault()
          }
        })}
        onKeyDown={composeEventHandlers(triggerProps.onKeyDown, (event) => {
          if (disabled) return
          if (["Enter", " "].includes(event.key)) context.onOpenToggle()
          if (event.key === "ArrowDown") context.onOpenChange(true)
          // Keeps the key from scrolling the page and from reaching the first item once it has focus
          if (["Enter", " ", "ArrowDown"].includes(event.key)) event.preventDefault()
        })}
      />
    </MenuPrimitive.Anchor>
  )
})

type DropdownMenuContentProps = Omit<MenuPrimitive.MenuContentProps, "onEntryFocus">

const DropdownMenuContent = forwardRef<DropdownMenuContentProps>((props, forwardedRef) => {
  const context = useDropdownMenuContext("DropdownMenuContent")
  const hasInteractedOutsideRef = useRef(false)
  return (
    <MenuPrimitive.Content
      id={context.contentId}
      aria-labelledby={context.triggerId}
      {...props}
      ref={forwardedRef}
      onCloseAutoFocus={composeEventHandlers(props.onCloseAutoFocus, (event) => {
        if (!hasInteractedOutsideRef.current) context.triggerRef.current?.focus()
        hasInteractedOutsideRef.current = false
        // Focus is either placed here or left to the browser
        event.preventDefault()
      })}
      onInteractOutside={composeEventHandlers(props.onInteractOutside, (event) => {
        const originalEvent = event.detail.originalEvent as PointerEvent
        const isRightClick = originalEvent.button === 2 || (originalEvent.button === 0 && originalEvent.ctrlKey === true)
        // A modal menu blocks the page, so the user went nowhere and focus returns to the trigger
        if (!context.modal || isRightClick) hasInteractedOutsideRef.current = true
      })}
    />
  )
})

export { DropdownMenu as Root, DropdownMenuTrigger as Trigger, DropdownMenuContent as Content }
export {
  Portal,
  Group,
  Label,
  Item,
  CheckboxItem,
  RadioGroup,
  RadioItem,
  ItemIndicator,
  Separator,
  Sub,
  SubTrigger,
  SubContent,
} from "./internal/menu"
export type { DropdownMenuContentProps }
