import { options, type ComponentChildren, type FunctionComponent, type Ref } from "preact"

// Upstream (React 19) and Preact 11 pass `ref` to a function component as an ordinary prop. Preact 10 strips it and hands the ref
// the component instance, so move it into the props. Preact 11 never sets vnode.ref on a function component: nothing happens there.
const previous = options.vnode
options.vnode = (vnode) => {
  const { type } = vnode
  if (vnode.ref && typeof type === "function" && !type.prototype?.render) {
    (vnode.props as { ref?: unknown }).ref = vnode.ref
    vnode.ref = null
  }
  previous?.(vnode)
}

export function forwardRef<P extends { ref?: Ref<any> }>(
  render: (props: Omit<P, "ref">, ref: P["ref"]) => ComponentChildren
): FunctionComponent<P> {
  return (props: any) => {
    const { ref, ...rest } = props
    return render(rest, ref)
  }
}
