# verification.md — canonical template

Evidence before assertions, always. The `Result:` line is machine-read
(phase derivation and close entry both key on it) — never omit it, never
leave it stale.

## Template

```markdown
# Verification Report: <change-name>

- **Date:** YYYY-MM-DD
- **Mode:** light | full (why: <scale rule that picked it>)
- **Range:** <base_ref short>..HEAD on `<branch>`

Result: <pass | fail>

With accepted deviations, append the count on that same machine-read line:
`Result: pass (2 accepted deviations)`. Derivation and close entry match the
canonical pass forms only; the count distinguishes a caveated pass from a clean one.
An active repair or design revisit invalidates an old passing report with
`Result: superseded (repair <date>)` or `Result: superseded (revision <date>)`
before changing the candidate; the completed verification replaces it.

## Scenario evidence

| Requirement / Scenario | Verdict | Evidence (literal command + output excerpt) |
|---|---|---|
| <capability>: <scenario name or Scenario-ID> | pass/fail | `$ cmd` → `output` |

For each scenario you verify, also record the structured claim (hashes only,
never argv): `onto evidence record <change> --task N --scenario <Scenario-ID>
--exec <name> --cmd-hash <sha256 of the command line> --exit <n> --output
<file>`. The binary never runs the command — you run it, then record; that
keeps verification inside the host's permission checks. `N` is the numeric
marker from the task's `[trace #N]`, not its dotted `N.M` plan ID.

## Design conformance

<key design decisions walked against the implementation; deviations are
findings, not footnotes>

## Adversarial pass

- Conformance skeptic: <verdict summary + findings triaged>
- Robustness skeptic: <verdict summary + findings triaged>
- Evidence pack: <readable artifact/output paths, base/candidate OIDs, supported
  environments, non-goals and confirmed pack consumption>

| Finding ID | Supported scenario / requirement | Evidence and impact | Coordinator severity / disposition | Next action / closure evidence |
|---|---|---|---|---|
| <stable ID> | <contract or grounded invariant> | <probe or file:line> | <repair / evidence request / justified dismissal / authorized deviation> | <task/probe and result> |

<For a repair round, identify changed claims and adjacent regression risks;
carry prior dispositions forward unless new evidence or changed scope reopens them.>
<!-- Or record "light: no skeptic pass" / an unavailable independent pass here,
     with reason and direct checks performed. Explicit independent-review
     requirements still block; denial or a blocked worker is not absence.
     Direct review is never labeled a completed skeptic verdict. -->

## Regression

<full build/test suite command + literal result; if the project has no
suite, state that fact — it is a result, not a skip>

## Deviations

<each accepted deviation + rationale + who accepted it; empty section
stays present reading "none">
```

## Rules

- Every verdict cites candidate-bound output validated for THIS verify round.
  Reuse only under the execution policy's provenance/input rules; no bare "passed
  earlier" or stale logs. Finalize the report before recording artifact-bound receipts.
- A scenario that cannot be demonstrated is a **fail**, not a skip.
- With no delta files, full and light changes declare each scenario once
  in `tasks.md` or `verification.md`, on an unfenced standalone line such as
  `Scenario-ID: handoff-content`. Describe the observable check and cite that ID
  in the evidence table; table mentions and fenced examples are not declarations.
  Retain the explicit no-spec justification in the workflow review. Do not invent
  behavioral specs, downgrade a full workflow, or omit required spec changes.
- When any delta files exist, their scenario declarations are authoritative;
  tasks/report declarations are not a fallback for missing delta IDs. Unknown or
  duplicate IDs are rejected by `onto evidence record` before a receipt is written.
- Accepted deviations keep `Result: pass`; they live in Deviations, never
  in the enum.
