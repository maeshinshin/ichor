# 0074. UI mobile layout: wrapping header, and a scrollbar gutter instead of scroll-lock padding

Status: Accepted · Date: 2026-10-26 · Design section: UI
Decided: The header wraps on phones, with the nav on its own row that scrolls inside itself; the modal scroll lock stops padding the body, and `html` gets `scrollbar-gutter: stable`

## Context

On a ~390px phone the logo, five nav links and the theme button did not fit in one row, so the page grew wider than the viewport. Opening a Dialog or Select then locked body scroll in `ui/src/lib/radix/internal/modal-layer.ts`, which added `padding-right` equal to `window.innerWidth - documentElement.clientWidth`. With an overflowing or zoomed-out mobile layout viewport that difference is not a scrollbar, so the body got a large right padding and the content collapsed into a narrow column.

## Decision

1. The header is `flex-wrap`; below `sm` the nav is `order-last w-full overflow-x-auto`, so it can never widen the page. From `sm` up the layout is the previous single row.
2. The scroll lock no longer measures a scrollbar or pads the body; it only sets `overflow: hidden` and restores it. `html { scrollbar-gutter: stable }` in `index.css` reserves the gutter whether or not the body overflows, so a classic scrollbar hiding under a dialog shifts nothing, and it is a no-op for overlay scrollbars on phones. (An `offsetWidth - clientWidth` measurement was rejected: the viewport owns the scrollbar, so on `<html>` it reads about 0.)
3. `DialogContent` gets `max-h-[calc(100dvh-2rem)] overflow-y-auto` so tall dialogs fit short screens.

## Consequences

- No new component or i18n key; a hamburger menu is not needed because the nav row scrolls.
