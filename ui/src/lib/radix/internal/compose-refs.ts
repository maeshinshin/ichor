import type { Ref } from "preact"
import { useCallback } from "preact/hooks"

type PossibleRef<T> = Ref<T> | undefined

function setRef<T>(ref: PossibleRef<T>, node: T | null) {
  if (typeof ref === "function") ref(node)
  else if (ref) ref.current = node
}

export function composeRefs<T>(...refs: PossibleRef<T>[]) {
  return (node: T | null) => {
    for (const ref of refs) setRef(ref, node)
  }
}

export function useComposedRefs<T>(...refs: PossibleRef<T>[]) {
  return useCallback(composeRefs(...refs), refs)
}
