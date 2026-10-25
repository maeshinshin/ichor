import { useCallback, useRef, useState } from "preact/hooks"

type Updater<T> = T | ((prev: T) => T)

export function useControllableState<T>({
  prop,
  defaultProp,
  onChange,
}: {
  prop?: T
  defaultProp: T
  onChange?: (value: T) => void
}) {
  const [uncontrolled, setUncontrolled] = useState(defaultProp)
  const isControlled = prop !== undefined
  const value = isControlled ? prop : uncontrolled

  const latest = useRef({ value, isControlled, onChange })
  latest.current = { value, isControlled, onChange }

  const setValue = useCallback((next: Updater<T>) => {
    const current = latest.current
    const nextValue = typeof next === "function" ? (next as (prev: T) => T)(current.value) : next
    if (Object.is(nextValue, current.value)) return
    if (!current.isControlled) {
      latest.current = { ...current, value: nextValue }
      setUncontrolled(() => nextValue)
    }
    current.onChange?.(nextValue)
  }, [])

  return [value, setValue] as const
}
