import { Component, render, type ComponentChildren } from "preact"

class ContextProvider extends Component<{ context: object; children?: ComponentChildren }> {
  override getChildContext() {
    return this.props.context
  }
  render() {
    return this.props.children
  }
}

// Preact only has portals in preact/compat: render a second tree whose DOM parent forwards to body (no wrapper element)
// and re-provide this tree's context so hooks inside see the same providers. Nothing renders on the server.
class Portal extends Component<{ children?: ComponentChildren }> {
  host = {
    nodeType: 1,
    namespaceURI: "http://www.w3.org/1999/xhtml",
    childNodes: [],
    contains: () => true,
    insertBefore: (child: Node, before: Node | null) => document.body.insertBefore(child, before),
    removeChild: (child: Node) => document.body.removeChild(child),
  }

  override componentDidMount() {
    this.renderChildren()
  }
  override componentDidUpdate() {
    this.renderChildren()
  }
  override componentWillUnmount() {
    render(null, this.host as any)
  }
  // Preact numbers `useId` per render root, so this second tree would repeat the ids of the tree around it. Hand it that tree's
  // counter before every render, because each render replaces the root that carries it.
  // shortcut: reads Preact's minified internals (`__v` vnode, `__` parent, `__m` id counter, `__k` rendered tree), the same in 10.29
  // and 11.0. If a release renames them this does nothing and ids repeat across the portal (the id test in portal.test.tsx then fails
  // on that version); Preact 11's core createPortal renders inside one tree, so it needs none of this
  shareIds() {
    let vnode = (this as any).__v
    while (vnode && !vnode.__m && vnode.__) vnode = vnode.__
    if (!vnode) return
    const host = this.host as any
    host.__k ||= {}
    host.__k.__m = vnode.__m ||= [0, 0]
  }
  renderChildren() {
    this.shareIds()
    render(<ContextProvider context={this.context}>{this.props.children}</ContextProvider>, this.host as any)
  }
  render() {
    return null
  }
}

export { ContextProvider, Portal as Root }
