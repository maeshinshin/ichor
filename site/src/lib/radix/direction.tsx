import { createContext, type ComponentChildren } from "preact"
import { useContext } from "preact/hooks"

type Direction = "ltr" | "rtl"

const DirectionContext = createContext<Direction | undefined>(undefined)

function DirectionProvider({ dir, children }: { dir?: Direction; children?: ComponentChildren }) {
  return <DirectionContext.Provider value={dir}>{children}</DirectionContext.Provider>
}

function useDirection(localDir?: Direction): Direction {
  const contextDir = useContext(DirectionContext)
  return localDir || contextDir || "ltr"
}

export { useDirection, DirectionProvider }
export type { Direction }
