import { cloneElement, h, type ComponentProps, type JSX, type VNode } from "preact"
import { composeRefs } from "../../radix/internal/compose-refs"
import { getElementRef, isElement } from "../../radix/internal/element"
import { mergeStyles } from "../../radix/internal/style"

// @shadcn/react 0.3.1 use-render: its stand-in for Base UI's useRender on Preact. Same props merge and state attributes.

type State = Record<string, unknown>
type Props = Record<string, any>
type RenderFunction<S extends State> = (props: Props, state: S) => VNode<any> | null
type RenderProp<S extends State> = VNode<any> | RenderFunction<S>
type StateAttributesMapping<S extends State> = {
  [K in keyof S]?: (value: S[K]) => Record<string, string | undefined> | null | undefined
}

type UseRenderComponentProps<E extends keyof JSX.IntrinsicElements, S extends State = State> = ComponentProps<E> & {
  render?: RenderProp<S>
}

const isEventHandler = (key: string) => /^on[A-Z]/.test(key)

// Later sources win, except class names (joined; `class` and `className` are one), styles (merged), refs and event handlers (both run,
// the later source first and the earlier one unless it was prevented)
function mergeProps(...sources: (Props | undefined)[]) {
  const result: Props = {}
  for (const source of sources) {
    if (!source) continue
    for (const key of Object.keys(source)) {
      const value = source[key]
      if (value === undefined) continue
      const current = result[key]
      if (key === "className" || key === "class") {
        result.className = [result.className, value].filter(Boolean).join(" ")
      } else if (key === "style") {
        result.style = mergeStyles(current, value)
      } else if (key === "ref") {
        result.ref = current ? composeRefs(current, value) : value
      } else if (isEventHandler(key) && typeof current === "function" && typeof value === "function") {
        result[key] = (event: Event) => {
          value(event)
          if (!event.defaultPrevented) current(event)
        }
      } else {
        result[key] = value
      }
    }
  }
  return result
}

function getStateAttributes<S extends State>(state: S, mapping?: StateAttributesMapping<S>) {
  const props: Props = {}
  for (const key of Object.keys(state) as (keyof S & string)[]) {
    const value = state[key]
    const attributes = mapping?.[key]?.(value)
    if (attributes) {
      Object.assign(props, attributes)
      continue
    }
    const attribute = `data-${key.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)}`
    if (typeof value === "boolean") props[attribute] = value ? "" : undefined
    else if (value !== null && value !== undefined) props[attribute] = String(value)
  }
  return props
}

function useRender<S extends State = State>({
  defaultTagName,
  props,
  render,
  state = {} as S,
  stateAttributesMapping,
}: {
  defaultTagName: string
  props?: Props
  render?: RenderProp<S>
  state?: S
  stateAttributesMapping?: StateAttributesMapping<S>
}) {
  const elementProps = mergeProps(getStateAttributes(state, stateAttributesMapping), props)
  if (!render) return h(defaultTagName, elementProps)
  if (typeof render === "function") return render(elementProps, state)
  if (!isElement(render)) return null

  // The element's own ref lives on the vnode (Preact 10 host elements) or in its props
  const { ref: _ref, ...renderProps } = render.props
  const ref = [elementProps.ref, getElementRef(render)].filter(Boolean)
  return cloneElement(render, { ...mergeProps(elementProps, renderProps), ref: ref.length ? composeRefs(...ref) : undefined })
}

export { mergeProps, useRender }
export type { RenderProp, UseRenderComponentProps }
