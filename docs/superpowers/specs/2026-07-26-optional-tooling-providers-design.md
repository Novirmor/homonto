---
comet_change: optional-tooling-providers
role: technical-design
canonical_spec: openspec
---

# Optional tooling providers — technical design

Deepens `openspec/changes/optional-tooling-providers/design.md`. That document
fixes the direction (a `[tooling]` table rendered into a generated sidecar);
this one fixes the mechanics, the migration, and the test strategy.

## 1. Config layer

`internal/config` gains a `Tooling` struct carried on `Config`:

```go
type Tooling struct {
    ShellProxy string `toml:"shell_proxy"`
    CodeIntel  string `toml:"code_intel"`
}
```

Unknown keys inside `[tooling]` are rejected by the existing strict-decode
path — no bespoke handling. Value validation lives in `validate.go` alongside
the other closed-set checks and follows the v0.8.0 precedent of naming the
offender rather than failing anonymously:

```
tooling.code_intel "ctags" is not a known provider (accepted: graphify, okf, none)
```

An empty string resolves to `none` at resolve time, not at decode time, so an
absent table and an explicit `none` are indistinguishable to every downstream
consumer. That keeps the default path and the opt-out path on identical code.

## 2. Provider fragments

Provider prose lives in `catalog/tooling/`:

```
catalog/tooling/rtk.md
catalog/tooling/graphify.md
catalog/tooling/okf.md
catalog/tooling/none-shell-proxy.md
catalog/tooling/none-code-intel.md
```

Each fragment is the body of one preflight step — pure prose, no frontmatter,
because fragments are not skills. They are homonto-authored descriptions of
how to use a tool the user installs themselves, so ADR 0015 is satisfied: no
third-party content is vendored.

Embedding is one added pattern in `catalog/embed.go`:

```go
//go:embed all:frameworks all:skills all:commands all:subagents all:tooling version.txt
```

The two rules the fragments absorb from today's dispatcher prose, because they
are provider behavior rather than dispatcher behavior:

- warn once per change, not once per dispatch, keyed on the change's
  `notes.md` Grounding section;
- staleness counts as absence for an index-backed provider.

## 3. Renderer

```go
func RenderTooling(t config.Tooling) ([]byte, error)
```

Emits a generated header naming both resolved providers and stating that
edits are overwritten on the next apply, then the shell-proxy fragment, then
the code-intel fragment. The order is fixed, so output is deterministic for a
given pair — a requirement for the fingerprint to mean anything.

When both resolve to `none`, the two `none-*` fragments produce a file that
states no providers are declared and that grounding falls back to direct file
reading. The file always exists; only its content varies. A skill that points
at a reference which sometimes does not exist would be a worse contract than
one that always resolves.

## 4. Materialization

`Materialize` learns the resolved tooling config and the dispatcher set. For
each dispatcher skill, it writes `references/tooling.md` into the **staging**
directory after the verbatim walk and before the atomic swap, so the existing
stage-then-swap gives crash safety for free: a partial sidecar is never
swapped into place.

**Dispatcher identification** uses the established convention — the dispatcher
is the skill whose name equals its framework's name. This already holds for
both shipped frameworks and is documented in `catalog/frameworks/onto/
framework.toml`. No new schema key is introduced.

## 5. Materialize gate

```go
toolingFP = sha256(shellProxy, codeIntel, selected fragment bytes)
fingerprint = subagentRenderFingerprint(renderCtx) + ":" + contentFP + ":" + toolingFP
```

The fragment bytes are digested here rather than in `ContentFingerprint`
because that function digests per **named resource** (`skill:onto`,
`command:onto`), and fragments are not a declared resource. Digesting only the
*selected* fragments means editing an unselected one causes no churn, while
editing a selected one re-renders.

Without this component, editing `[tooling]` leaves the catalog version and all
resource bytes unchanged, so the gate reports up to date and serves the stale
sidecar forever — the same defect class the content fingerprint was introduced
to fix in v0.2.1.

`allSkillDirsExist` becomes dispatcher-aware: for a dispatcher skill it also
asserts `references/tooling.md` exists, so hand-deleting the sidecar triggers
repair instead of being masked by an up-to-date fingerprint.

## 6. State migration

`State.SubagentRenderFingerprint` is renamed to `RenderFingerprint`
(`json:"renderFingerprint,omitempty"`) and `CurrentStateSchemaVersion` goes
from 1 to 2.

**No value-preserving migration is written, deliberately.** The field is a
cache whose documented semantics are already "absent = force re-render", and
this change bumps the catalog version, which forces re-materialization for
every user on upgrade regardless. Copying the old value would therefore buy
nothing while adding a shadow-field code path that could itself be wrong. A
schema-1 state file simply loads with the new field empty and re-renders once.

The accompanying doc comment is corrected: the field has held a composite
since v0.2.1, and now holds three components.

## 7. Doctor

`homonto doctor` gains a warning-level finding when `[tooling]` declares a
provider that is not present:

- `shell_proxy = "rtk"` → `exec.LookPath("rtk")`
- `code_intel = "graphify"` → `graphify-out/` or `.codegraph/` present, or the
  skill installed
- `code_intel = "okf"` → the okf scripts or a generated bundle present

Detection is `LookPath` and file-existence only. Nothing is executed, so none
of the 30-second exec-timeout and hung-prompt concerns from v0.7.0 apply. The
finding is a warning, never a failure — a declared provider that is missing is
a degraded workflow, not a broken projection, consistent with ADR 0008.

## 8. Neutralizing shipped catalog content

Eleven files stop naming providers:

| File | Change |
|---|---|
| `skills/onto/SKILL.md` | preflight steps 1 and 2 collapse into one pointer to `references/tooling.md` |
| `skills/onto-open/SKILL.md` + `references/notes.md` + `references/proposal.md` | provider-neutral grounding wording |
| `skills/onto-design/SKILL.md` + `references/brainstorm-protocol.md` + `references/design.md` | same |
| `skills/onto-close/references/lint-checklist.md` | same |
| `commands/onto.md` | preflight description drops the two names |
| `subagents/onto-explorer.md`, `subagents/to-explorer.md` | "your configured code-intelligence provider" |
| the `to` dispatcher | gains the same pointer |

Phase skills refer to "your configured code-intelligence provider" and point
at the dispatcher's reference rather than restating provider steps.

## 9. Test strategy

- **Config**: table-driven over absent, partial, full, invalid value, and
  unknown key.
- **Renderer**: golden files for all six combinations (2 shell proxy × 3 code
  intel), each asserting the undeclared provider's name is absent.
- **Gate**: editing `[tooling]` re-renders; an unchanged config is a no-op;
  deleting the sidecar restores it.
- **Migration**: a schema-1 state file loads and forces exactly one re-render.
- **Neutrality**: a test walking `catalog/` asserting no provider name appears
  outside `catalog/tooling/`. Scoped to `catalog/` so legitimate mentions in
  `docs/` do not trip it.
- **Doctor**: declared-and-absent yields a finding; declared-and-present and
  `none` yield none.
- **E2E**: the onto and to Docker lifecycle suites assert the sidecar exists
  and matches the declared providers.

## 10. Risks

| Risk | Mitigation |
|---|---|
| Instruction silently lost while moving prose out of 11 files | Fragments start as **verbatim** moves, reviewed as a pure move; wording changes are a separate later task |
| Existing users lose graphify grounding on upgrade | Release note plus the two-line restore snippet |
| Sidecar hand-deleted and never repaired | Dispatcher-aware presence check (§5) |
| Neutrality test over-matches | Scoped to `catalog/`, excluding `catalog/tooling/` |
| Scope creep toward a provider plugin API | Closed value sets; adding a provider is a deliberate catalog change |

## 11. Spec Patches

Two supplementary scenarios are written back to
`specs/tooling-providers/spec.md`. Neither changes scope or structure:

1. Under the referenced-never-installed requirement: a declared-but-absent
   provider produces a warning-level doctor finding and never blocks apply.
2. Under the re-render requirement: a schema-version-1 state file loads and
   forces one re-render rather than failing.
