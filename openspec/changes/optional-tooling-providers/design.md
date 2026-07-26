## Context

The `onto` and `to` dispatcher skills run a tooling preflight before any phase
work. ADR 0005 introduced `rtk` and `graphify` as recommended tools; ADR 0008
downgraded them from halting requirements to warn-never-halt. Neither ADR
removed their names from the shipped prose, so today 11 catalog files name them
directly and every user is told about both regardless of what they run.

Two existing mechanisms make this tractable:

- **ADR 0006 reference-file skill architecture** — a skill loads a
  `references/*.md` file on demand instead of inlining a protocol. The onto
  skills already use this for the no-slop, subagent, TDD, and debugging
  protocols.
- **Config-driven render at materialize time** —
  `MaterializeSubagents(dstRoot, names, renderCtx)` in
  `internal/catalog/materialize.go` already renders per-tool agent variants
  from config rather than copying bytes verbatim.

Materialization is gated in `internal/engine/engine.go` by a composite
fingerprint:

```
fingerprint = subagentRenderFingerprint(renderCtx) + ":" + contentFP
```

recorded in `State.SubagentRenderFingerprint`, combined with the catalog
version and a presence check over every file a materialize would write.

## Goals / Non-Goals

**Goals**

- Provider choice becomes configuration, expressed once in `homonto.toml`.
- A user who declares nothing is told about nothing.
- `okf` becomes a first-class alternative to `graphify` without homonto
  taking on any responsibility for installing it.
- Provider prose lives in exactly one place and both frameworks share it.

**Non-Goals**

- Installing, updating, or version-checking providers.
- A provider plugin API. The value sets are closed.
- Changing the warn-never-halt rule from ADR 0008.
- Per-phase or per-skill provider overrides.

## Decisions

### D1. Generated sidecar, not a splice into SKILL.md

The chosen mechanism generates `references/tooling.md` inside each materialized
dispatcher skill. `SKILL.md` ships byte-stable and says only "run the tooling
preflight described in `references/tooling.md`".

*Why:* the shipped skill stays a verbatim catalog artifact, so
`ContentFingerprint` keeps its current meaning, every install has identical
`SKILL.md` bytes (supportable, diffable), and it reuses ADR 0006's established
pattern.

*Alternative rejected:* splicing a marked region inside `SKILL.md` at
materialize time. It removes one file read and lets absent providers vanish
entirely, but it turns the most-read file in the framework into per-install
generated content and entangles the content fingerprint with render inputs.

### D2. Provider prose lives in the catalog, not in Go

Each provider gets a fragment at `catalog/tooling/<provider>.md`. The Go layer
selects the two declared fragments, concatenates them under a generated
header, and writes the result. Go holds no provider prose.

*Why:* keeps content in the catalog where ADR 0015 says content belongs;
`ContentFingerprint` already digests catalog source bytes, so editing a
fragment re-materializes for free; prose changes review as prose.

*Alternative rejected:* provider descriptions as Go string constants. Prose in
code, invisible to the content fingerprint, and every wording fix becomes a
binary release.

### D3. Extend the composite fingerprint with a tooling component

```
fingerprint = subagentRenderFingerprint(renderCtx) + ":" + contentFP + ":" + toolingFP
```

`toolingFP` digests the two resolved provider names. Without this, editing
`[tooling]` leaves the catalog version and content bytes unchanged, so the
gate reports up-to-date and serves the stale sidecar forever — the same defect
class the content fingerprint was added to fix in v0.2.1.

Note: `State.SubagentRenderFingerprint` already holds a composite rather than
a subagent-only value, so this makes an existing misnomer worse. Renaming the
persisted JSON field is a state-schema migration and is deliberately out of
scope here (see Open Questions).

### D4. The presence check covers the generated sidecar

`allSkillDirsExist` only stats the skill directory. A user who deletes just
`references/tooling.md` would keep an up-to-date fingerprint and never get the
file back. The check gains a per-skill assertion that the generated sidecar
exists for dispatcher skills.

### D5. Only dispatcher skills receive the sidecar

`onto` and `to` are the only skills that run the preflight. Phase skills that
currently mention a provider get a pointer to the dispatcher's reference
instead of their own copy, so there is one generated file per framework.

### D6. Closed value sets validated at load

`shell_proxy` accepts `rtk` or `none`; `code_intel` accepts `graphify`, `okf`,
or `none`. An unknown value or an unknown key inside `[tooling]` fails at load
naming the offender, matching the v0.8.0 precedent of naming the offending
table instead of failing anonymously.

### D7. Providers are referenced, never vendored

`okf` names okf-generator (`UmairBaig8/okf-generator`) as a selectable
provider. homonto never downloads, installs, or executes it, exactly as it
never installed `rtk` or `graphify`. ADR 0015 bars third-party content from the
catalog, and the provider fragment is homonto-authored prose describing how to
use a tool the user installs themselves.

## Risks / Trade-offs

- **Existing users silently lose graphify grounding** (defaults become `none`)
  → the release note calls it out explicitly, and the change ships with the
  one-line config snippet that restores the old behavior.
- **A user deletes the generated sidecar and the skill points at nothing** →
  mitigated by D4's presence check.
- **Provider prose drifts between the two frameworks** → mitigated by D2: both
  frameworks render from the same `catalog/tooling/` fragments.
- **`to` and `onto` dispatchers diverge in how they reference the sidecar** →
  the generator writes the same filename and header into both; a test asserts
  both dispatchers point at it.
- **Scope creep into a provider plugin API** → the closed value sets in D6 are
  the guard; adding a provider is a deliberate catalog change.

## Migration Plan

1. No config migration. An absent `[tooling]` table stays valid, so every
   existing config keeps loading.
2. Catalog and both framework versions bump, so the next `homonto apply`
   re-materializes and writes the sidecar.
3. Users who want today's behavior add:

   ```toml
   [tooling]
   shell_proxy = "rtk"
   code_intel  = "graphify"
   ```

4. Rollback is a downgrade plus one apply; nothing persists outside the
   materialized catalog and the fingerprint field, both of which are rewritten
   on the next run.

## Open Questions

1. **Should `doctor` report a declared-but-absent provider?** It fits the
   existing doctor finding pattern and is cheap. Recommendation: yes, as a
   warning-level finding, but it can be split into a follow-up change if it
   expands this one past its verification budget.
2. **Rename `State.SubagentRenderFingerprint` to a neutral name?** It has held
   a composite since v0.2.1 and this change adds a third component.
   Recommendation: defer — a persisted-field rename needs a state-schema
   migration that this change does not otherwise require.
3. **Does the `to` framework want a shell-proxy provider at all?** Its
   dispatcher mentions the tools only through `to-explorer`. Recommendation:
   render the same sidecar for symmetry, and let the fragment text stay
   framework-neutral.
