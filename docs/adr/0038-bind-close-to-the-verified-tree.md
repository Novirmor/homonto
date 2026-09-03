# Bind close to the verified tree

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

Verification passed at some commit A, but nothing tied the pass to the tree
that was later archived. Commits landing after the pass — including unverified
source changes — still closed cleanly, because the close gate checked only the
recorded result token, the report line, and worktree cleanliness.

## Decision

`onto set verify-result <change> pass` freezes each scoped repository's HEAD
into `verify.heads` (alias `""` is the config repository). Recording a pass
outside a git repository is refused; inside one, any capture failure is a loud
refusal, never a silently unbound pass. Close and archive recovery require
each head to still resolve as a canonical commit id, remain reachable from
HEAD, and have only workflow bookkeeping after it — rename detection is off,
so moving a source file into `docs/` still shows its deleted origin. In the
config repository, commits touching anything outside the workflow trees
(`docs/changes/`, `docs/specs/`, `docs/adr/`, `docs/guides/`, `.homonto/`)
refuse the close; in a selected sibling repository any commit at all refuses,
because the workflow keeps no tree there. Recording a new pass re-binds to the
new HEAD; any other result clears the heads together with the close evidence
they justified. States written before the field carry no heads and stay
closeable (the same legacy tolerance the integration marker uses).

## Consequences

The archived tree is the verified tree up to declared bookkeeping. A
post-verification source commit blocks close until the change is re-verified,
so evidence cannot be recycled across edits. Fixing a late defect now follows
the reopen path (invalidate, fix, re-verify) instead of silently riding an old
pass. The binding is best-effort at capture (a workspace outside git records
no heads) but strict at close whenever heads exist.
