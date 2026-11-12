# 0045. UI behaviour that can hurt a cluster is tested as pure functions in ui/src/lib, not through rendered pages

Status: Accepted · Date: 2026-10-26 · Design section: UI, Roadmap phase 3
Decided: Test the UI's dangerous decisions (may this approval proceed, may this patch be sent, how is an error shown) as pure functions in `ui/src/lib/` under `node --test`, with no component test framework and no `ui/src/gen` import in those files; the page screens stay untested by unit tests

## Context

`ui/src/pages/` had no unit tests; only `resumableStream` and
`patchTarget` were covered. The screens that can do real damage are the
approval dialog (ADR 0021) and the patch editor (ADR 0039).

The UI is preactcn-only, with no React and no `preact/compat`. The usual
component-testing libraries assume React, so they cannot be added. A DOM
harness for Preact would be a new dependency, and `node --test` runs with
no bundler. Generated-client imports without a file extension already
fail there with `ERR_MODULE_NOT_FOUND`.

## Decision

Decisions go in `ui/src/lib/` as pure functions and are tested there. The
page calls them and does nothing else with the result.

- `canApprove` (identity.ts): approval needs a strong identity (uuid,
  serial, a MAC) and a chosen role. The dialog shows identity, hardware
  and role before this gate opens.
- `buildPatchTarget` and `canSavePatch` (patchTarget.ts): malformed
  selector JSON throws, so nothing is sent. Only the chosen target field
  is populated. The cluster layer ignores the target.
- `describeError` (errors.ts): the apiserver's validation message is
  shown verbatim, under a headline chosen by status code.

Files in `lib/` that take generated types use `import type` only, or take
the enum reading as a boolean from the caller.

Not covered, on purpose: the screens' rendering, and the delete
confirmation. Delete only runs from the button inside the confirmation
dialog, so no code path skips it. Testing that needs a DOM. A browser
end-to-end test is the right tool and a separate decision.

## Consequences

- `describeError` uses `ConnectError.rawMessage`, not `.message`, which
  carries a `[code]` prefix and would keep the apiserver text from being
  shown verbatim.
- Wiring mistakes inside a page (a gate not called, wrong prop) are not
  caught. Only the rule itself is.
- New dangerous UI decisions are extracted into `lib/` first.
