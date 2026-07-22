---
comet_change: onto-gate-enforcement
role: technical-design
canonical_spec: openspec
---

# onto-gate-enforcement — Design Doc

Finish the onto binary's enforcement story: the judgment gates get evidence
tokens, the verify result gets a report cross-check, phase derivation moves
into tested Go, and presets reach build through one gated call. Delta spec:
`openspec/changes/onto-gate-enforcement/specs/onto-evidence-gates/spec.md`
(canonical); this doc is the implementation design.

## Context

`internal/ontocli/gate.go` (`pendingGates`) covers seven bookkeeping
decisions; the judgment gates (proposal approval, approach confirmation,
close confirmation) are prose-only. `advance.go` gates on artifacts, checked
tasks, `verify.result`, isolation, dep cycles, and dirt — but `verify.result`
is self-asserted and never checked against `verification.md`.
`ontostate.DerivePhase()` (state.go:215) validates and returns the *claimed*
phase, so `onto state --json`'s existing `derived_phase` key echoes the
cache it is named to distrust. The fix/tweak skills script two ceremonial
`onto advance` calls.

## D1 — Evidence tokens

**State** (`internal/ontostate/state.go`, gated core):

```go
ProposalApproved string `yaml:"proposal_approved,omitempty" json:"proposal_approved,omitempty"`
ApproachConfirmed string `yaml:"approach_confirmed,omitempty" json:"approach_confirmed,omitempty"`
CloseConfirmed   string `yaml:"close_confirmed,omitempty" json:"close_confirmed,omitempty"`
```

Free-form values (convention `YYYY-MM-DD <summary>`); Validate() imposes no
shape beyond what YAML gives (B1: presence, not judgment). No schema-version
bump: absent fields decode empty.

**Setters** (`set.go`): three subcommands on the `directiveCmd` free-form
pattern — `proposal-approved`, `approach-confirmed`, `close-confirmed`,
each `<change> <evidence>` with a non-empty check.

**Advance** (`advance.go`, in the phase-evidence gate block):

- leaving `open`, workflow full (or ""): `ProposalApproved == ""` → refuse:
  `cannot leave open: proposal approval not recorded (run onto set
  proposal-approved <name> "<evidence>")`.
- entering `build` (design exit), workflow full: `ApproachConfirmed == ""` →
  refuse analogously, checked alongside the existing isolation gate.
- Presets skip both (their scope gate lives in the preset skill; blocking
  presets on design tokens would contradict the preset's reason to exist).

**Close** (`mergedeltas.go`, `close.go`): first check after state
load/validate — `CloseConfirmed == ""` → refuse before any filesystem
mutation, all workflows.

**Gate surface** (`gate.go`): three new `pendingGate` entries — open/full →
`proposal-approved`; design/full → `approach-confirmed` (ordered before
isolation, the question the user answers first); close → `close-confirmed`
(ordered first in close). Free-form gates carry no fixed options; the
`SetCommand` names the evidence argument.

## D2 — Verify cross-check

In `advance.go`, after the `verify.result` check when leaving verify: read
`<changeDir>/verification.md`; scan for a line with prefix `Result: `. Refuse
when the file is unreadable (`cannot leave verify: reading verification.md`),
when no `Result:` line exists, or when the line does not start with
`Result: pass` (tolerating the `(N accepted deviations)` suffix by prefix
match): `cannot leave verify: verification.md says %q but verify.result=pass
— fix the report or the state, they must agree`.

## D3 — Truthful derivation

New `ontostate.DeriveWorkingPhase(changeDir string, st State) (string, error)`
implementing the dispatcher's evidence table, first match wins:

1. `st.Archived` → `done`
2. design.md contains line-prefix `Status: Under revision` → `design`
3. verification.md contains line-prefix `Result: pass` → `close`
4. tasks.md has ≥1 checkbox and none unchecked → `verify`
5. design.md contains `Status: Confirmed`, or workflow is a preset → `build`
6. workflow full: tasks.md exists (incomplete) or design.md exists
   (unconfirmed) → `design`
7. workflow full: proposal.md exists, claimed phase ∈ {open, design} →
   claimed phase (the open↔design boundary has no file signal)
8. otherwise → `open`

File reads are line-prefix scans (same tolerance the skills specify);
missing files are "absent", not errors. `DerivePhase()` keeps its
validate-and-echo behavior for callers that want the claimed phase, but
`statecmd.go` switches `derived_phase` to `DeriveWorkingPhase` and adds
`phase_mismatch bool` (derived ≠ claimed). `status.go` rows append
` (working: <derived>)` when mismatched.

## D4 — Preset `--to build`

`advance.go` gains `--to <phase>` (only `build` accepted). Refusals:
workflow full (`full changes advance one gate at a time`), target ≠ build.
Implementation: loop `runAdvance` single hops until `st.Phase == "build"`,
reloading state each hop; a failing hop's error propagates unchanged, so
every gate still fires.

## D5 — Skills + docs

- `onto-open`: artifact-review gate records `onto set proposal-approved`;
  exit checklist adds it before `onto advance`.
- `onto-design`: approach gate records `onto set approach-confirmed`; exit
  checklist adds it beside isolation.
- `onto-close`: final gate records `onto set close-confirmed`; step 3 notes
  merge-deltas/close refuse without it.
- `onto-fix`/`onto-tweak`: scripted double advance → `onto advance <name>
  --to build`.
- `onto` dispatcher §3: consume `onto state <name> --json`
  (`derived_phase`, `phase_mismatch`); the evidence table remains as
  documentation of what the binary computes, marked as such.
- `docs/guides/onto-workflow.md`: gate table + in-flight migration note.
- `framework.toml` 0.5.0 → 0.6.0.

## Testing

Refuse/pass unit-test pairs per gate; derivation tests per table row plus
the claimed-phase rule and preset default; cross-check cases (report fail,
deviations suffix, missing file, missing Result line); `--to build`
happy/refusal/mid-hop-failure; full suite; onto-lifecycle E2E extended
(missing-token refusal, cross-check refusal, `--to build` leg).

## Risks

- In-flight full changes block on their next advance until the skill records
  the missing token — intended (the unanswered gate is re-asked), documented.
- A dishonest `onto set` can still fabricate evidence — out of scope; the
  target is the honest agent that skips (B1).
- `--to build` must never soften a hop's gates — covered by the mid-hop
  failure test.
