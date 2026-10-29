import { BubbleInput, useFormControl } from "./internal/bubble-input"
import { composeEventHandlers } from "./internal/compose-event-handlers"
import { createStrictContext } from "./internal/create-context"
import { forwardRef } from "./internal/forward-ref"
import { Primitive, type PrimitiveProps, type PrimitiveWith } from "./internal/primitive"
import { useControllableState } from "./internal/use-controllable-state"

const [SwitchProvider, useSwitchContext] = createStrictContext<{ checked: boolean; disabled?: boolean }>("Switch")

const getState = (checked: boolean) => (checked ? "checked" : "unchecked")

type SwitchProps = PrimitiveWith<
  "button",
  {
    checked?: boolean
    defaultChecked?: boolean
    disabled?: boolean
    required?: boolean
    name?: string
    value?: string
    onCheckedChange?(checked: boolean): void
  }
>

const Switch = forwardRef<SwitchProps>(
  ({ checked: checkedProp, defaultChecked, disabled, required, name, value = "on", onCheckedChange, ...switchProps }, forwardedRef) => {
    const [checked, setChecked] = useControllableState({
      prop: checkedProp,
      defaultProp: defaultChecked ?? false,
      onChange: onCheckedChange,
    })
    const { control, ref, isFormControl } = useFormControl(forwardedRef)

    return (
      <SwitchProvider value={{ checked, disabled }}>
        <Primitive.button
          type="button"
          role="switch"
          aria-checked={checked}
          aria-required={required}
          data-state={getState(checked)}
          data-disabled={disabled ? "" : undefined}
          disabled={disabled}
          value={value}
          {...switchProps}
          ref={ref}
          onClick={composeEventHandlers(switchProps.onClick, () => setChecked((prev) => !prev))}
        />
        {isFormControl && (
          <BubbleInput type="checkbox" control={control} checked={checked} name={name} value={value} required={required} disabled={disabled} />
        )}
      </SwitchProvider>
    )
  }
)

const SwitchThumb = forwardRef<PrimitiveProps<"span">>((props, ref) => {
  const context = useSwitchContext("SwitchThumb")
  return <Primitive.span data-state={getState(context.checked)} data-disabled={context.disabled ? "" : undefined} {...props} ref={ref} />
})

export { Switch as Root, SwitchThumb as Thumb }
