import { createContext, type ComponentChildren } from "preact"
import { useContext } from "preact/hooks"

// Prefresh (dev-server HMR in Vite and Fresh) rewrites calls to the imported createContext into a cache keyed by call site and
// first parameter, so every context sharing a rootName ("Accordion" twice, each createCollection) would become one object.
// Calling through an alias keeps its babel plugin from matching.
const create = createContext

export function createStrictContext<T>(rootName: string) {
  const Context = create<T | undefined>(undefined)
  const Provider = (props: { value: T; children?: ComponentChildren }) => (
    <Context.Provider value={props.value}>{props.children}</Context.Provider>
  )
  function useStrictContext(consumerName: string) {
    const context = useContext(Context)
    if (context === undefined) throw new Error(`\`${consumerName}\` must be used within \`${rootName}\``)
    return context
  }
  return [Provider, useStrictContext] as const
}
