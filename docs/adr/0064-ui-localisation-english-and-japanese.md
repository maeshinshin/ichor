# 0064. The UI is localised in English and Japanese with a typed dictionary and no library

Status: Superseded by 0070 · Date: 2026-10-26 · Design section: UI, Roadmap phase 10
Decided: The UI's fixed wording lives in two typed objects, `en` and `ja`, in `ui/src/lib/i18n.ts`, read through one `t()` function, with no i18n dependency; the language is the saved choice, else `navigator.languages`, else English, switched on screen and kept in `localStorage` with nothing on the server; names, field names, state words from Argo CD or the cluster, log lines and the body of apiserver or Talos errors are shown as received; dates and sizes go through `Intl` for the chosen language

## Context

Roadmap phase 10 lists "UI localisation". The users include Japanese speakers. The UI is Preact with
preactcn; React and `preact/compat` are not allowed (project rule), which rules out the React-based i18n
libraries (react-i18next, react-intl, LinguiJS's React bindings). The screens hold about 220 fixed strings.

## Decision

**1. No library.** `ui/src/lib/i18n.ts` has `en` (an `as const` object) and `ja`, typed
`Record<keyof typeof en, string>`. A key missing from `ja`, or only in `ja`, does not compile. `translate(lang,
key, params)` replaces `{name}` markers. Run-time checks also require that both have the same keys, that no
entry is empty, and that both fill the same placeholders, because a compiler check does not see a `ja` entry
that lost a `{n}`. There are no plural forms: the strings are written so a count is only ever a number in
a label ("Pending approval (3)"), which both languages can say without grammar. If a language with plural
rules is added, `Intl.PluralRules` is already in the platform and the cost is one function.

**2. Choosing the language.** `lang.ts` keeps the current language in a module variable. At first use it is
`localStorage["ichor-ui-lang"]` if that is `en` or `ja`, else the first tag of `navigator.languages` whose primary
subtag is `en` or `ja`, else English. A selector in the page header changes it, writes `localStorage`, sets
`<html lang>` and redraws. Each language is named in itself ("English", "日本語"). Storage and `navigator` are read
behind try/catch, so a blocked store only means the choice does not outlive the tab. Nothing is sent to the
server and no server setting exists: the language is a property of the person, not of the installation.
Components call `useT()`, which subscribes them to a change; plain functions (`describeError`, `formatBytes`,
`enumLabel`) read the current language directly.

**3. What is translated.** The screens' own fixed text: titles, column headings, buttons, hints, empty and
loading states, dialog text, the headline `describeError` puts above an error, and the display names of the proto
enums (`HandoverPhase`, `ConfigLayer`, ...). An enum value without a dictionary entry still falls back to its
title-cased proto name, so a value added to the API later shows up readable, not missing.

Not translated, on purpose:

- Names of custom resources, namespaces, hostnames, and Kubernetes, Talos and proto field names (`TalosConfigPatch`,
  `matchLabels`, `ichorUI.oidc`, the document kinds `SysctlConfig` and so on), which a person must be able to type
  or search for exactly.
- The body of an error from the apiserver, Talos or ichor-ui (ADR 0045). `describeError` translates only the
  headline; the detail is shown as received, so what is searched for in the docs or an issue is what the server said.
- State words that other systems return (Argo CD's `Synced`, `OutOfSync`, `Healthy`; condition `type` and `status`)
  and the log lines.
- The CLI. This ADR is about the UI; the CLI's output is what scripts and bug reports quote.

**4. Dates and numbers.** `formatTimestamp` and `formatBytes` take the language and use `Intl.DateTimeFormat`
(`dateStyle: medium`, `timeStyle: medium`) and `Intl.NumberFormat`. Sizes stay binary (1024) and keep the labels
`B KB MB GB TB` in both languages: the unit symbols are the same in Japanese usage, and changing the base or the
labels per language would make one number read as two sizes.

## Consequences

- No dependency, no build step, and no bundle growth beyond the strings (a few KB).
- Adding a language is one object and one entry in `langs` / `langNames`; the compiler lists what is missing.
- The English text is the source of truth. A change to an English string does not tell the translator; reviewers
  change both objects in one commit, which the key check enforces only for presence, not for meaning.
- Strings are translated whole, with placeholders, so word order differs by language where it has to.
  The one sentence with a link in it (the effective-config note) is stored in three pieces around the link.
- The Japanese wants a read-through by a native speaker.
- The language logic is pure functions in `ui/src/lib`, as ADR 0045 prescribes.
