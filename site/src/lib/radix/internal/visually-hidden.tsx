import { forwardRef } from "./forward-ref"
import { Primitive, type PrimitiveProps } from "./primitive"
import { mergeStyles } from "./style"

// Invisible but still announced by screen readers
export const VisuallyHidden = forwardRef<PrimitiveProps<"span">>((props, ref) => (
  <Primitive.span
    {...props}
    ref={ref}
    style={mergeStyles(
      { position: "absolute", border: 0, width: "1px", height: "1px", padding: 0, margin: "-1px", overflow: "hidden", clip: "rect(0, 0, 0, 0)", whiteSpace: "nowrap", wordWrap: "normal" },
      props.style
    )}
  />
))
