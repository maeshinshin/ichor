import type { ComponentChildren, RefObject } from "preact"
import { useId, useRef, useState } from "preact/hooks"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { useDismissableLayer, type DismissableLayerProps } from "./internal/dismissable-layer"
import { useFocusScope } from "./internal/focus-scope"
import { forwardRef } from "./internal/forward-ref"
import { useModalLayer } from "./internal/modal-layer"
import { useNonModalLayer } from "./internal/non-modal-layer"
import * as PortalPrimitive from "./internal/portal"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { useControllableState } from "./internal/use-controllable-state"

const [DialogProvider, useDialogContext] = createStrictContext<{
  triggerRef: RefObject<HTMLButtonElement | null>
  overlayRef: RefObject<HTMLDivElement | null>
  contentId: string
  titleId: string
  descriptionId: string
  open: boolean
  modal: boolean
  onOpenChange(open: boolean): void
  onOpenToggle(): void
}>("Dialog")

const getState = (open: boolean) => (open ? "open" : "closed")

// shortcut: no Portal `container` or `forceMount`; add them if a caller needs to portal elsewhere or keep the dialog mounted
function Dialog({
  open: openProp,
  defaultOpen,
  onOpenChange,
  modal = true,
  children,
}: {
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?(open: boolean): void
  modal?: boolean
  children?: ComponentChildren
}) {
  const triggerRef = useRef<HTMLButtonElement>(null)
  const overlayRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useControllableState({ prop: openProp, defaultProp: defaultOpen ?? false, onChange: onOpenChange })
  return (
    <DialogProvider
      value={{
        triggerRef,
        overlayRef,
        contentId: useId(),
        titleId: useId(),
        descriptionId: useId(),
        open,
        modal,
        onOpenChange: setOpen,
        onOpenToggle: () => setOpen((prev) => !prev),
      }}
    >
      {children}
    </DialogProvider>
  )
}

const DialogTrigger = forwardRef<PrimitiveProps<"button">>((props, forwardedRef) => {
  const context = useDialogContext("DialogTrigger")
  const ref = useComposedRefs(forwardedRef, context.triggerRef)
  return (
    <Primitive.button
      type="button"
      aria-haspopup="dialog"
      aria-expanded={context.open}
      aria-controls={context.open ? context.contentId : undefined}
      data-state={getState(context.open)}
      {...props}
      ref={ref}
      onClick={composeEventHandlers(props.onClick, context.onOpenToggle)}
    />
  )
})

function DialogPortal({ children }: { children?: ComponentChildren }) {
  return <PortalPrimitive.Root>{children}</PortalPrimitive.Root>
}

const DialogOverlay = forwardRef<PrimitiveProps<"div">>((props, forwardedRef) => {
  const context = useDialogContext("DialogOverlay")
  const ref = useComposedRefs(forwardedRef, context.overlayRef)
  return context.modal ? (
    <Presence present={context.open}>
      <Primitive.div data-state={getState(context.open)} {...props} ref={ref} />
    </Presence>
  ) : null
})

type DialogContentProps = PrimitiveWith<
  "div",
  DismissableLayerProps & { onOpenAutoFocus?(event: Event): void; onCloseAutoFocus?(event: Event): void }
>

const DialogContentImpl = forwardRef<DialogContentProps>(
  (
    { onOpenAutoFocus, onCloseAutoFocus, onEscapeKeyDown, onPointerDownOutside, onFocusOutside, onInteractOutside, ...contentProps },
    forwardedRef
  ) => {
    const context = useDialogContext("DialogContent")
    const [node, setNode] = useState<HTMLElement | null>(null)
    const ref = useComposedRefs(forwardedRef, setNode)
    const nonModal = useNonModalLayer(context.triggerRef, { onCloseAutoFocus, onInteractOutside })
    const { modal } = context

    // Registered before the scope moves focus, so a layer below does not take that focus for one leaving it
    useDismissableLayer(node, {
      onEscapeKeyDown,
      onPointerDownOutside: modal
        ? composeEventHandlers(onPointerDownOutside, (event) => {
            const { button, ctrlKey } = event.detail.originalEvent
            // A right click, or ctrl+click on macOS, acts like a context menu on the overlay and must not dismiss
            if (button === 2 || (button === 0 && ctrlKey)) event.preventDefault()
          })
        : onPointerDownOutside,
      onFocusOutside: modal ? composeEventHandlers(onFocusOutside, (event) => event.preventDefault()) : onFocusOutside,
      onInteractOutside: modal ? onInteractOutside : nonModal.onInteractOutside,
      onDismiss: () => context.onOpenChange(false),
    })
    // The scope must see the trigger as focused before the modal layer makes it inert
    useFocusScope(node, {
      loop: true,
      onMountAutoFocus: onOpenAutoFocus,
      onUnmountAutoFocus: modal
        ? composeEventHandlers(onCloseAutoFocus, (event) => {
            // Without a trigger (command palette, sidebar sheet) the scope returns focus to what had it before opening
            if (!context.triggerRef.current) return
            event.preventDefault()
            context.triggerRef.current.focus()
          })
        : nonModal.onUnmountAutoFocus,
    })
    useModalLayer(modal ? node : null, context.overlayRef)

    return (
      <Primitive.div
        role="dialog"
        id={context.contentId}
        aria-describedby={context.descriptionId}
        aria-labelledby={context.titleId}
        data-state={getState(context.open)}
        tabIndex={-1}
        {...contentProps}
        ref={ref}
      />
    )
  }
)

const DialogContent = forwardRef<DialogContentProps>((props, ref) => {
  const context = useDialogContext("DialogContent")
  return (
    <Presence present={context.open}>
      <DialogContentImpl {...props} ref={ref} />
    </Presence>
  )
})

const DialogClose = forwardRef<PrimitiveProps<"button">>((props, ref) => {
  const context = useDialogContext("DialogClose")
  return <Primitive.button type="button" {...props} ref={ref} onClick={composeEventHandlers(props.onClick, () => context.onOpenChange(false))} />
})

const DialogTitle = forwardRef<PrimitiveProps<"h2">>((props, ref) => {
  const context = useDialogContext("DialogTitle")
  return <Primitive.h2 id={context.titleId} {...props} ref={ref} />
})

const DialogDescription = forwardRef<PrimitiveProps<"p">>((props, ref) => {
  const context = useDialogContext("DialogDescription")
  return <Primitive.p id={context.descriptionId} {...props} ref={ref} />
})

export {
  Dialog as Root,
  DialogTrigger as Trigger,
  DialogPortal as Portal,
  DialogOverlay as Overlay,
  DialogContent as Content,
  DialogClose as Close,
  DialogTitle as Title,
  DialogDescription as Description,
}
export type { DialogContentProps }
