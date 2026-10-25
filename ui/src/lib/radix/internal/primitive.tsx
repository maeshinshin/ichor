import { h, type FunctionComponent, type JSX, type Ref } from "preact"
import { Root as Slot } from "../slot"
import { forwardRef } from "./forward-ref"
import { joinClass } from "./merge-props"

const TAGS = ["a", "button", "div", "h2", "h3", "img", "label", "p", "span", "svg"] as const

type Tag = (typeof TAGS)[number]
// Like Radix, a part accepts a ref of any element type: a ref typed for one element would not fit asChild or wrappers around it
export type PrimitiveProps<T extends Tag> = Omit<JSX.IntrinsicElements[T], "ref"> & { asChild?: boolean; ref?: Ref<any> }

export type PrimitiveWith<T extends Tag, Own> = Omit<PrimitiveProps<T>, keyof Own> & Own

function createPrimitive<T extends Tag>(tag: T) {
  return forwardRef<PrimitiveProps<T>>(({ asChild, class: classProp, className, ...props }: any, ref) =>
    h(asChild ? Slot : tag, { ...props, class: joinClass(classProp, className), ref })
  ) as FunctionComponent<PrimitiveProps<T>>
}

export const Primitive = Object.fromEntries(TAGS.map((tag) => [tag, createPrimitive(tag)])) as {
  [T in Tag]: FunctionComponent<PrimitiveProps<T>>
}
