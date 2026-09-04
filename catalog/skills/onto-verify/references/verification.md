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
A mid-build design revisit writes `Result: superseded (revision <date>)` to
invalidate the report; the verify phase does not write that value.

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
