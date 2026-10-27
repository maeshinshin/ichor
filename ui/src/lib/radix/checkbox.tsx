import { BubbleInput, useFormControl } from "./internal/bubble-input"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import { Presence } from "./internal/presence"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { mergeStyles } from "./internal/style"
import { useControllableState } from "./internal/use-controllable-state"

type CheckedState = boolean | "indeterminate"

const [CheckboxProvider, useCheckboxContext] = createStrictContext<{ state: CheckedState; disabled?: boolean }>("Checkbox")

const getState = (checked: CheckedState) => (checked === "indeterminate" ? "indeterminate" : checked ? "checked" : "unchecked")

type CheckboxProps = PrimitiveWith<
  "button",
  {
    checked?: CheckedState
    defaultChecked?: CheckedState
    disabled?: boolean
    required?: boolean
    name?: string
    value?: string
    onCheckedChange?(checked: CheckedState): void
  }
>

const Checkbox = forwardRef<CheckboxProps>(
  ({ checked: checkedProp, defaultChecked, disabled, required, name, value = "on", onCheckedChange, ...checkboxProps }, forwardedRef) => {
    const [checked, setChecked] = useControllableState<CheckedState>({
      prop: checkedProp,
      defaultProp: defaultChecked ?? false,
      onChange: onCheckedChange,
    })
    const { control, ref, isFormControl } = useFormControl(forwardedRef)

    return (
      <CheckboxProvider value={{ state: checked, disabled }}>
        <Primitive.button
          type="button"
          role="checkbox"
          aria-checked={checked === "indeterminate" ? "mixed" : checked}
          aria-required={required}
          data-state={getState(checked)}
          data-disabled={disabled ? "" : undefined}
          disabled={disabled}
          value={value}
          {...checkboxProps}
          ref={ref}
          onKeyDown={composeEventHandlers(checkboxProps.onKeyDown, (event) => {
            // According to WAI ARIA, checkboxes don't activate on enter keypress
            if (event.key === "Enter") event.preventDefault()
          })}
          onClick={composeEventHandlers(checkboxProps.onClick, () => setChecked((prev) => (prev === "indeterminate" ? true : !prev)))}
        />
        {isFormControl && (
          <BubbleInput type="checkbox" control={control} checked={checked} name={name} value={value} required={required} disabled={disabled} />
        )}
      </CheckboxProvider>
    )
  }
)

const CheckboxIndicator = forwardRef<PrimitiveProps<"span">>((props, ref) => {
  const context = useCheckboxContext("CheckboxIndicator")
  return (
    <Presence present={context.state !== false}>
      <Primitive.span
        data-state={getState(context.state)}
        data-disabled={context.disabled ? "" : undefined}
        {...props}
        ref={ref}
        style={mergeStyles({ pointerEvents: "none" }, props.style)}
      />
    </Presence>
  )
})

export { Checkbox as Root, CheckboxIndicator as Indicator }
