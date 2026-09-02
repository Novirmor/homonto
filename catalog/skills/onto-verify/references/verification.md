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
- **Result: pass | fail** — with accepted deviations, append the count:
  `Result: pass (2 accepted deviations)`. Derivation and close entry match
  on the `Result: pass` prefix; the count keeps a caveated pass visibly
  different from a clean one. (A third value, `superseded (revision
  <date>)`, is written by a mid-build design revisit to invalidate this
  report — never by the verify phase itself.)

## Scenario evidence

| Requirement / Scenario | Verdict | Evidence (literal command + output excerpt) |
|---|---|---|
| <capability>: <scenario name or Scenario-ID> | pass/fail | `$ cmd` → `output` |

For each scenario you verify, also record the structured claim (hashes only,
never argv): `onto evidence record <change> --task N --scenario <Scenario-ID>
--exec <name> --cmd-hash <sha256 of the command line> --exit <n> --output
<file>`. The binary never runs the command — you run it, then record; that
keeps verification inside the host's permission checks.

## Design conformance

<key design decisions walked against the implementation; deviations are
findings, not footnotes>

## Adversarial pass

- Conformance skeptic: <verdict summary + findings triaged>
- Robustness skeptic: <verdict summary + findings triaged>
<!-- or: "skipped: <reason>" — protocol-mandated skips (no dispatch
     capability; light-mode optional) are recorded HERE only and need no
     acceptor; they do not go in Deviations -->

## Regression

<full build/test suite command + literal result; if the project has no
suite, state that fact — it is a result, not a skip>

## Deviations

<each accepted deviation + rationale + who accepted it; empty section
stays present reading "none">
```

## Rules

- Every verdict cites fresh output from THIS verify round — no "passed
  earlier", no stale logs.
- A scenario that cannot be demonstrated is a **fail**, not a skip.
- Accepted deviations keep `Result: pass`; they live in Deviations, never
  in the enum.
