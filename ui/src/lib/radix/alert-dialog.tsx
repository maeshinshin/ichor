import type { ComponentProps, RefObject } from "preact"
import { useRef } from "preact/hooks"
import * as DialogPrimitive from "./dialog"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { useComposedRefs } from "./internal/compose-refs"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import type { PrimitiveProps } from "./internal/primitive"

// Always modal, so unlike Dialog it has no `modal` prop
function AlertDialog(props: Omit<ComponentProps<typeof DialogPrimitive.Root>, "modal">) {
  return <DialogPrimitive.Root {...props} modal />
}

const [AlertDialogContentProvider, useAlertDialogContentContext] = createStrictContext<{ cancelRef: RefObject<HTMLButtonElement | null> }>("AlertDialogContent")

// Built on Dialog: it is always modal, ignores outside pointer interaction and starts with focus on Cancel
const AlertDialogContent = forwardRef<DialogPrimitive.DialogContentProps>((props, ref) => {
  const cancelRef = useRef<HTMLButtonElement>(null)
  return (
    <AlertDialogContentProvider value={{ cancelRef }}>
      <DialogPrimitive.Content
        role="alertdialog"
        {...props}
        ref={ref}
        onOpenAutoFocus={composeEventHandlers(props.onOpenAutoFocus, (event) => {
          // Without a Cancel the page behind is inert, so keep the default focus instead of leaving it on the trigger
          if (!cancelRef.current) return
          event.preventDefault()
          cancelRef.current.focus({ preventScroll: true })
        })}
        onInteractOutside={(event) => event.preventDefault()}
      />
    </AlertDialogContentProvider>
  )
})

const AlertDialogCancel = forwardRef<PrimitiveProps<"button">>((props, forwardedRef) => {
  const { cancelRef } = useAlertDialogContentContext("AlertDialogCancel")
  return <DialogPrimitive.Close {...props} ref={useComposedRefs(forwardedRef, cancelRef)} />
})

export { Trigger, Portal, Overlay, Title, Description, Close as Action } from "./dialog"
export { AlertDialog as Root, AlertDialogContent as Content, AlertDialogCancel as Cancel }
