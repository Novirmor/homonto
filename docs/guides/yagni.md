# YAGNI — you aren't gonna need it

Build what the change needs now; nothing for a future that hasn't asked.
Speculative capability is the most expensive code in an agent workflow: it is
written without a requirement, reviewed without a spec, and maintained
forever. Both workflow frameworks encode YAGNI structurally. This guide
names where, so you can lean on the mechanism instead of your discipline.

## The framework choice is YAGNI at the largest scale

onto and `to` are [complementary](to-workflow.md#onto-and-to--complementary-by-selection):
pick the workflow per change. If a change doesn't need evidence gates, spec
deltas, and a dependency graph, putting it through onto anyway is a YAGNI
violation you pay for on
every change. Pick `to`; switch the repo to onto when a real requirement —
audits, multi-change dependencies, non-skippable verification — shows up, not
before.

## YAGNI in onto

- **Presets before the full workflow.** A bug fix is `onto new --workflow
  fix`; a small non-bug change is `--workflow tweak`. Both skip the design
  phase entirely; design ceremony for a two-file fix is speculative
  structure. The upgrade gate exists precisely so you can start minimal and
  escalate only when the work demands it (scope exceeding a single
  function/module, a new capability appearing).
- **The proposal states what it will NOT do.** The open phase's scope
  boundary is a YAGNI contract: work outside it during build is a
  scope-change gate, not a quiet expansion.
- **Tasks are outcomes, not provisions.** A task like "add extension points
  for later" fails the template's test: it has no verification, because
  nothing uses it. A task that can't state its proof isn't ready to build.
- **Deferral is bounded.** `- [x] N.N DEFERRED to close:` exists for
  non-runtime bookkeeping only. There is deliberately no "defer to some
  future change" marker. Future work belongs in a future change's proposal,
  where it must justify itself.
- **Mid-build discoveries are appended, not expanded.** The live task list
  forces discovered work to become a named, verifiable task. "While I'm here"
  work that no task names is prohibited. If it matters, it earns a task; if
  it can't justify a task, you didn't need it.

## YAGNI in to

- **The whole framework is applied YAGNI.** Three phases, one gate command,
  one artifact. When planning inside `to` starts sprouting design documents,
  dependency notes between changes, or evidence tables, stop: that is
  onto-shaped work, and the `to-plan` skill says so explicitly: do not
  rebuild onto inside a `plan.md`.
- **The plan's boundary line.** Every plan opens with what the change
  deliberately does not do. Work that breaks that boundary is a scope change
  the user confirms — never "discovered work".
- **Documentation tasks only when a promise changes.** The plan includes the
  smallest documentation task when the implementation contradicts an
  existing guide or ADR, and no design ceremony for details no document
  promises.
- **Implementers do exactly the handed task.** The `to-implementer` contract
  is the smallest change that satisfies the task; adjacent work is reported,
  never done. `to-reviewer` treats silent scope creep as a finding.

## YAGNI when writing the code itself

Both frameworks' implementer contracts encode the same three rules:

1. The smallest change that satisfies the task; no unrelated refactors.
2. No capability without a caller: no flags nobody passes, no abstraction
   with one implementation, no config for values that never vary.
3. If you think you'll need it later, write it down (a proposal, a plan
   task, a `## Notes` line) instead of building it now. Recorded intent is
   free; speculative code is not.

The test, always: **name the requirement this serves today.** If the answer
starts with "eventually" or "someone might", it goes in writing, not in code.

See also: [KISS](kiss.md) — YAGNI bounds *what* you build; KISS bounds *how*.
