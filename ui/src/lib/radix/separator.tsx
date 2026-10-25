import { forwardRef } from "./internal/forward-ref"
import { Primitive, type PrimitiveProps } from "./internal/primitive"

type Orientation = "horizontal" | "vertical"
type SeparatorProps = PrimitiveProps<"div"> & { orientation?: Orientation; decorative?: boolean }

const Separator = forwardRef<SeparatorProps>(({ decorative, orientation: orientationProp, ...domProps }, ref) => {
  const orientation = orientationProp === "vertical" ? "vertical" : "horizontal"
  const semanticProps = decorative
    ? ({ role: "none" } as const)
    : ({ role: "separator", "aria-orientation": orientation === "vertical" ? "vertical" : undefined } as const)
  return <Primitive.div data-orientation={orientation} {...semanticProps} {...domProps} ref={ref} />
})

export { Separator as Root }
