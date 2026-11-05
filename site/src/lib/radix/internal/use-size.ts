import { useLayoutEffect, useState } from "preact/hooks"

type Size = { width: number; height: number }

export function useSize(element: HTMLElement | null) {
  const [size, setSize] = useState<Size | undefined>(undefined)

  useLayoutEffect(() => {
    if (!element) return setSize(undefined)
    setSize({ width: element.offsetWidth, height: element.offsetHeight })
    const observer = new ResizeObserver((entries) => {
      const box = entries[0]?.borderBoxSize?.[0]
      if (box) setSize({ width: box.inlineSize, height: box.blockSize })
      else setSize({ width: element.offsetWidth, height: element.offsetHeight })
    })
    observer.observe(element, { box: "border-box" })
    return () => observer.unobserve(element)
  }, [element])

  return size
}
