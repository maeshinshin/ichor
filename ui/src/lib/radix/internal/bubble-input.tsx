import type { Ref } from "preact"
import { useEffect, useRef, useState } from "preact/hooks"
import { useComposedRefs } from "./compose-refs"
import { useSize } from "./use-size"

type BubbleInputProps = {
  type: "checkbox" | "radio"
  control: HTMLElement | null
  checked: boolean | "indeterminate"
  name?: string
  value?: string
  required?: boolean
  disabled?: boolean
}

// Before the control mounts (and during SSR) assume a form so server-rendered forms submit without JavaScript
export function useFormControl<T extends HTMLElement>(forwardedRef: Ref<T> | undefined) {
  const [control, setControl] = useState<T | null>(null)
  const ref = useComposedRefs(forwardedRef, setControl)
  return { control, ref, isFormControl: control ? !!control.closest("form") : true }
}

// A real input that mirrors the control, so native submission, validation and form-level input/change listeners work
export function BubbleInput({ control, checked, ...inputProps }: BubbleInputProps) {
  const ref = useRef<HTMLInputElement>(null)
  const previous = useRef(checked)
  const size = useSize(control)

  useEffect(() => {
    if (previous.current === checked) return
    previous.current = checked
    ref.current?.dispatchEvent(new Event("input", { bubbles: true }))
    ref.current?.dispatchEvent(new Event("change", { bubbles: true }))
  }, [checked])

  return (
    <input
      aria-hidden="true"
      tabIndex={-1}
      checked={checked === true}
      indeterminate={checked === "indeterminate"}
      {...inputProps}
      // Preact 11 only types a literal here, and checkbox and radio take the same attributes
      type={inputProps.type as "checkbox"}
      ref={ref}
      // Sized and placed over the control so the browser anchors its validation message there
      style={{
        width: size && `${size.width}px`,
        height: size && `${size.height}px`,
        position: "absolute",
        pointerEvents: "none",
        opacity: 0,
        margin: 0,
        transform: "translateX(-100%)",
      }}
    />
  )
}
