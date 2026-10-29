import { forwardRef } from "./internal/forward-ref"
import { createStrictContext } from "./internal/create-context"
import { Primitive, type PrimitiveProps } from "./internal/primitive"

const MAX = 100

const [ProgressProvider, useProgressContext] = createStrictContext<{ value: number | null }>("Progress")

const getState = (value: number | null) => (value == null ? "indeterminate" : value === MAX ? "complete" : "loading")

const Progress = forwardRef<PrimitiveProps<"div"> & { value?: number | null }>(({ value: valueProp, ...props }, ref) => {
  const value = typeof valueProp === "number" && valueProp >= 0 && valueProp <= MAX ? valueProp : null
  return (
    <ProgressProvider value={{ value }}>
      <Primitive.div
        aria-valuemax={MAX}
        aria-valuemin={0}
        aria-valuenow={value ?? undefined}
        aria-valuetext={value === null ? undefined : `${value}%`}
        role="progressbar"
        data-state={getState(value)}
        data-value={value ?? undefined}
        data-max={MAX}
        {...props}
        ref={ref}
      />
    </ProgressProvider>
  )
})

const ProgressIndicator = forwardRef<PrimitiveProps<"div">>((props, ref) => {
  const { value } = useProgressContext("ProgressIndicator")
  return <Primitive.div data-state={getState(value)} data-value={value ?? undefined} data-max={MAX} {...props} ref={ref} />
})

export { Progress as Root, ProgressIndicator as Indicator }
