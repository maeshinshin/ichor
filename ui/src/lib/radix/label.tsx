import { forwardRef } from "./internal/forward-ref"
import { Primitive, type PrimitiveProps } from "./internal/primitive"

const Label = forwardRef<PrimitiveProps<"label">>((props, ref) => <Primitive.label {...props} ref={ref} />)

export { Label as Root }
