import type { CSSProperties, HTMLAttributes } from "preact"

// shortcut: string styles are not parsed, so a string style is dropped when merged; add parsing if string styles show up
export function mergeStyles(...styles: HTMLAttributes["style"][]) {
  return Object.assign({}, ...styles.filter((style) => typeof style === "object")) as CSSProperties
}
