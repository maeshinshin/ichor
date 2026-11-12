# 0070. The UI is English only, with a light/dark switch

Status: Accepted · Date: 2026-10-26 · Design section: UI, Roadmap phase 10
Decided: The UI has one language, English. The `ja` dictionary, the language selector, and language detection and storage are removed; `en` stays in `ui/src/lib/i18n.ts` behind `t()` / `useT()`; dates and sizes use `Intl` with `en`, not the browser locale; the page header carries a light/dark switch that follows `prefers-color-scheme` until the user chooses, then keeps the choice in `localStorage`
Supersedes: 0064

## Context

ADR 0064 localised the UI in English and Japanese and picked the language from `navigator.languages`. The UI is English only: a Japanese browser should not produce a Japanese page. A dark/light switch is also wanted; `index.css` already has the `dark` custom variant and token block.

## Decision

**1. English only.** `ja`, `Lang`, `langs`, `langNames`, `detectLang`, `lang.ts`, the `LanguageSelector`/`LanguageSwitch` component and the `common.language` key are deleted. The English strings are unchanged. `t(key, params)` lives in `i18n.ts` and replaces `{name}` markers; `useT()` stays so the roughly 220 call sites do not change, but returns `t` without subscribing to anything. `enumName` and `formatBytes` / `formatDate` / `formatTimestamp` lose their `lang` parameter and use `en`.

**2. Light/dark switch.** `ui/src/lib/theme.ts` has the pure `chooseTheme(saved, prefersDark)`: a saved `light` or `dark` wins, otherwise the system preference. The choice is kept in `localStorage["ichor-ui-theme"]` (read and written in try/catch) and applied as the `dark` class on `<html>`. A small inline script in `ui/index.html` applies it before first paint to avoid a light flash. The header button is the preactcn `Button` and says which mode it switches to.

## Consequences

- Nothing in the UI follows the browser language. Reintroducing a language means a new ADR.
- The typed-dictionary indirection remains although only one dictionary exists; it keeps strings in one place and the call sites unchanged.
- The theme logic exists twice (TypeScript and the inline script); `theme.ts` says so.
