# Principles: YAGNI and KISS

Both engines encode two rules in their mechanics, so you can lean on the
process instead of on discipline:

- **YAGNI** — build only what the change needs now. It bounds *what*.
- **KISS** — build it the simplest way that works. It bounds *how*.

Speculative or clever code is doubly expensive in agent workflows: it's
written without a requirement, reviewed without a spec, and every future
session pays to understand it from scratch.

## YAGNI

### Choosing the engine

Putting a change through onto when it doesn't need evidence gates, spec
deltas, or a dependency graph is itself a YAGNI violation. Start with the
lightest path that meets the real obligations; onto presets upgrade to the
full workflow when the work demands it.

### In onto

- **Presets first.** A bug fix is `onto fix`, a small non-bug change is
  `onto tweak`; both skip design. Scope growth upgrades them.
- **The proposal says what it won't do.** Work outside that boundary during
  build is a scope decision, not a quiet expansion.
- **Tasks are outcomes with proof.** "Add extension points for later" can't
  state a verification, so it isn't a task.
- **Bounded deferral.** `DEFERRED TO CLOSE:` exists only for non-runtime
  bookkeeping; future work belongs in a future change's proposal.
- **Discovered work becomes a named task** before it's coded, or it isn't
  done.

### In to

- **The engine is applied YAGNI:** three phases, one artifact. When a plan
  starts sprouting design docs, cross-change dependencies, or evidence
  tables, it's onto-shaped work — `to-plan` says so instead of rebuilding
  onto inside `plan.md`.
- **Every plan opens with its boundary.** Breaking it needs your
  confirmation.
- **Docs tasks only when a promise changes** — when the implementation
  contradicts an existing guide or ADR.
- **Implementers do exactly the handed task.** Adjacent work is reported;
  reviewers treat silent scope creep as a finding.

### In review triage

Repairs are judged against your **current request**. Optional improvements
become recorded follow-ups, not code; a materially broader repair waits for
your approval.

### In code

1. The smallest change that satisfies the task; no unrelated refactors.
2. No capability without a caller: no flag nobody passes, no abstraction
   with one implementation, no config for a value that never varies.
3. If you'll "need it later", write it down (proposal, plan task, `## Notes`)
   instead of building it.

**Test:** name the requirement this serves today. If the answer starts with
"eventually" or "someone might", it goes in writing, not in code.

## KISS

### In onto

- **Simplest design that meets the requirements,** with the rejected
  alternatives recorded — an ADR documenting declined complexity is a KISS
  artifact.
- **Bite-sized tasks, one commit each.** One outcome per task; no "and" in a
  task title; a commit that's hard to review gets split.
- **Boring machinery.** onto itself uses a flat YAML state file, literal
  markers (`Status: Confirmed`, `- [ ]`), and exact-string gates. Prefer the
  same greppable style in your changes.
- **Reviews propose the smallest fix,** never a rewrite.

### In to

- **Plans under a screen.** Goal, boundary, a handful of task contracts, one
  `Final Verify:` line.
- **One artifact.** Planning, notes, review outcomes, and evidence all live in
  `plan.md`; no sidecar documents.
- **One writer per checkout.** Read-only agents run as concurrently as the
  work justifies; writing stays serialized per tree.
- **Match the surrounding code** — style, naming, idiom, comment density.

### In code

1. Solve it the obvious way first: standard library, the existing helper,
   the neighbouring file's pattern.
2. Flat over nested, explicit over implicit: early returns, named
   intermediates, duplication over the wrong abstraction.
3. Comments state constraints, not narration.
4. Simple prose too: the `onto-no-slop` / `to-no-slop` skills apply the same
   rules to the artifacts people keep.

**Test:** could a fresh session extend this without reading it twice? If
not, it isn't done.
