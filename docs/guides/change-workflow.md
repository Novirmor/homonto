# Change workflow

For work whose decisions have to survive the session. Three paths, chosen
by a human before anything is created.

```bash
homonto change start rework-catalog --request "Replace the catalog storage layer."
```

That does **not** start a change.

## Classification comes first

Starting a change opens a local, uncommitted **classification candidate**.
Read-only explorers and a skeptic inspect the request and the project,
Homonto suggests `fix`, `tweak`, or `full` and explains the evidence, and a
human confirms. No document, no change directory, and no portable record
exists until that confirmation — so abandoning a candidate leaves the
repository exactly as it was, and a suggestion is never mistaken for a
decision.

The suggested path is named in the prompt but is neither first nor default
among the choices, and choosing against it requires a rationale. A
confirmation you can give without reading is not a confirmation.

With no scope signal at all the suggestion is **tweak**, not fix. Fix
asserts something Homonto cannot observe — that the behaviour is a defect —
and asserting it for you would be a guess dressed as a classification.

## The three paths

### Full — `open → design → build → verify → close`

| Step | What happens |
|---|---|
| `open_explore` | Explorers establish current behaviour, constraints, affected repositories |
| `open_challenge` | A skeptic attacks the hidden assumptions |
| `open_draft` | You write `proposal.md` |
| `open_approve` | **You approve the scope** |
| `design_draft` | You write `design.md` and `tasks.md`, with acceptance criteria and ADR candidates |
| `design_challenge` | A skeptic attacks the design |
| `design_approve` | **You approve the design** |
| `build_plan` | You write `plan.md` |
| `build_implement` | Parallel implementers |
| `build_integrate` | One integration implementer per member |
| `verify_checks` | Homonto runs the checks |
| `verify_review` | Reviewer and skeptic |
| `verify_record` | Homonto generates `verification.md` |
| `build_repair` | A bounded repair round |
| `close_adr` | Required decision records are written |
| `close_finalize` | `record.md`, then archive |

Rejecting an approval sends the document back to be rewritten, in a new
generation so the gate can be asked again.

### Fix — `open → build → verify → close`

For an existing-behaviour defect. Its files are `fix.md`, `tasks.md`,
`verification.md`, `record.md`.

`fix.md` records the reproduction, the expected and actual behaviour, and
the root cause. A **failing automated test or a reproducible command is
required** before implementation:

```markdown
## Fix

reproduce: go test ./internal/catalog -run TestStaleRows

Expected: fresh rows. Actual: stale rows. Root cause: the cache key.
```

Homonto reads `reproduce:` or `failing test:` from the document. Without
one, you must approve the exception **with a rationale** — while sending it
back for a reproduction needs none, because asking for evidence requires no
justification. "We could not reproduce it" is exactly the condition under
which a fix is most likely to fix nothing.

### Tweak — `open → build → verify → close`

For a bounded behaviour, configuration, documentation, or prompt change.
Its files are `tweak.md`, `tasks.md`, `verification.md`, `record.md`.
`tweak.md` records the intent and the exact behaviour delta. A tweak never
reproduces: it has no defect, and asking for evidence of nothing is
ceremony.

Presets skip deep design and the full implementation plan. They do not skip
the explorers, the reviewer, the skeptic, or `verification.md` — every
Change path uses all four roles, and a preset's documents are lightweight,
not absent.

## When a preset outgrows itself

Any of these pauses a preset and asks you:

- a new capability
- a public API or storage-schema change
- a cross-module change
- a deep architectural change
- scope that should be split into several formal changes
- material expansion of the confirmed intent, even when nothing else matches
- **more than five changed non-test files**

The file count is a **warning, not a verdict**. It counts unique normalized
paths in the integrated diff from the immutable baseline captured when you
confirmed the path. A rename counts once. Generated, vendored, and test
paths are excluded. Continuation, repair, and a later reconfirmation never
move that baseline — which is what stops a preset escaping its own warning
by re-baselining.

Your two answers both need a rationale:

**Continue** — the preset proceeds with the broader scope recorded.
**Upgrade** — the change becomes Full.

Homonto never upgrades on your behalf.

## What an upgrade keeps

- `fix.md` or `tweak.md` survives as a **read-only preset input**. It is
  the record of what the change was before it grew.
- The old `tasks.md` is frozen as `preset-tasks.md`, which nobody may edit.
  The live list is cleared so Design writes a new one rather than
  inheriting a list scoped to a smaller change.
- `proposal.md` is created from the confirmed intent **and the reason it
  outgrew itself**.
- The change rewinds to Design and cannot reach implementation without a
  design approval.
- The immutable work baseline carries across untouched.

## Decision records

An ADR is owed when a change **establishes** a durable decision a future
maintainer could reasonably question. A candidate identified during Design
is not that: a question nobody answered leaves nothing to record. So a
requirement is a candidate plus the decision that settled it — which means
most changes owe nothing.

Declare candidates in the design (or a preset's input document):

```markdown
- adr-candidate: storage | Adopt the snapshot store | why is non-Git isolation a snapshot
```

Approving the design settles the candidates it identifies. Continuing a
preset past its tripwire, and fixing without a reproduction, are durable
too — and a preset's own decisions carry their own question, so they
synthesize a candidate rather than blocking.

A durable decision the design never identified sends a Full change **back
to Design**. Writing an ADR for a decision nobody designed would document
an accident.

Records land in `docs/homonto/adr/NNNN-slug.md`, four digits, never reused.
The number is reserved atomically, so two changes closing at once cannot
merge two decisions into one record. An empty reservation is not an ADR:
closing over one is refused.

## Invalidation

Per document, because the spec's rules differ sharply by document.

| What moved | Where it returns to |
|---|---|
| `proposal.md` | `open_draft` — the scope approval was of a document that no longer says that |
| `design.md` | `design_draft` |
| `tasks.md` (Full) | `design_draft` — tasks are a design output |
| `plan.md` | `build_plan` |
| `fix.md`, `tweak.md`, preset `tasks.md` | the preset draft — the path confirmation is re-run |
| the repository list | the first step |
| the path classes | the first step |
| the check configuration | the checks |
| an integrated source | the checks |
| the verification evidence | close only |

## Finishing

`record.md` is generated, and the change directory moves to
`docs/homonto/changes/archive/<date>-<name>/`. Integration branches and
staged directories are left ready; nothing is merged.
