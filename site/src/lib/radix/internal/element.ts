import type { VNode } from "preact"

export function isElement(value: unknown): value is VNode<any> {
  return typeof value === "object" && value !== null && "type" in value && "props" in value
}

export function getElementRef(element: VNode<any>) {
  return element.ref ?? element.props.ref
}
