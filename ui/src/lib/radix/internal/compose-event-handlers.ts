export function composeEventHandlers<E extends Event>(
  original: ((event: E) => void) | undefined,
  ours: ((event: E) => void) | undefined,
  { checkForDefaultPrevented = true } = {}
) {
  return (event: E) => {
    original?.(event)
    if (!checkForDefaultPrevented || !event.defaultPrevented) ours?.(event)
  }
}
