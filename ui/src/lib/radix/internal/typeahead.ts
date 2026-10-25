import { useCallback, useEffect, useRef } from "preact/hooks"

export function wrapArray<T>(array: T[], startIndex: number) {
  return array.map((_, index) => array[(startIndex + index) % array.length]!)
}

// Typing one letter repeatedly steps through the values starting with it; a longer search keeps matching from the current one
export function getNextMatch(values: string[], search: string, currentMatch?: string) {
  const isRepeated = search.length > 1 && Array.from(search).every((char) => char === search[0])
  const normalizedSearch = isRepeated ? search[0]! : search
  const currentIndex = currentMatch ? values.indexOf(currentMatch) : -1
  let wrapped = wrapArray(values, Math.max(currentIndex, 0))
  if (normalizedSearch.length === 1) wrapped = wrapped.filter((value) => value !== currentMatch)
  const nextMatch = wrapped.find((value) => value.toLowerCase().startsWith(normalizedSearch.toLowerCase()))
  return nextMatch !== currentMatch ? nextMatch : undefined
}

// The search builds up while keys keep coming and is forgotten one second after the last one
export function useTypeahead(onSearch: (search: string) => void) {
  const search = useRef("")
  const timer = useRef(0)
  const latest = useRef(onSearch)
  latest.current = onSearch

  const reset = useCallback(() => {
    search.current = ""
    clearTimeout(timer.current)
  }, [])
  const type = useCallback((key: string) => {
    search.current += key
    latest.current(search.current)
    clearTimeout(timer.current)
    timer.current = window.setTimeout(reset, 1000)
  }, [])
  useEffect(() => reset, [])

  return { search, type, reset }
}
