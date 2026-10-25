import { mergeStyles } from "./style"

type Props = Record<string, any>

export function joinClass(...values: unknown[]) {
  return values.filter(Boolean).join(" ") || undefined
}

export function mergeProps(slotProps: Props, childProps: Props) {
  const merged: Props = { ...slotProps }
  delete merged.class
  delete merged.className

  for (const key in childProps) {
    const slotValue = slotProps[key]
    const childValue = childProps[key]
    if (childValue === undefined || key === "class" || key === "className") continue
    if (/^on[A-Z]/.test(key) && typeof slotValue === "function" && typeof childValue === "function") {
      merged[key] = (...args: unknown[]) => {
        const result = childValue(...args)
        slotValue(...args)
        return result
      }
    } else if (key === "style") {
      merged[key] = mergeStyles(slotValue, childValue)
    } else {
      merged[key] = childValue
    }
  }

  const className = joinClass(slotProps.class, slotProps.className, childProps.class, childProps.className)
  // Converted components read `className` (as React's do), and Preact sets it on DOM elements too, so `class` is kept only
  // when the child spells it that way; a `class` handed to such a component would override its own `className`
  if (className) merged["class" in childProps && !("className" in childProps) ? "class" : "className"] = className
  return merged
}
