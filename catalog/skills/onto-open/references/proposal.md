# proposal.md — canonical template

Why + what + capability impact. No design content (that's design.md's job)
and no invented scope — everything traces to the confirmed clarification
summary.

## Template

```markdown
# Proposal: <change-name>

<!-- presets only: a `Preset: fix` / `Preset: tweak` line here, on its own
     line under the title — the rebuild greps `^Preset:`, so column-0
     position is what matters, not being byte one of the file. An upgrade
     rewrites it to `Preset: fix (upgraded to full YYYY-MM-DD)`. -->
<!-- optional: `Depends-on: <change-name>[, <change-name>]` — feeds onto-state.yaml deps -->

## Why

<problem/opportunity, 1–3 paragraphs; why now>

## What Changes

- <bulleted, specific; mark breaking changes **BREAKING**>

## Non-Goals

- <explicitly out of scope — capabilities, behaviors, users, platforms, or
  follow-ups this change will NOT address; traces to the confirmed
  clarification summary. Distinct from "Untouched" below, which is about
  which capability *spec files* are untouched, not behavioral scope>

## Capability Impact

- **New**: `<capability>` — <one line> (creates docs/specs/<capability>.md)
- **Modified**: `<capability>` — <which requirements change> (delta required)
- Untouched: <capabilities explicitly out of scope>

## Not split  <!-- only when the split-preflight considered a split -->

<why this stays one change>

## Grounding

<the sole record of open-phase grounding: code-intelligence queries run and
what they showed, file reads that anchor the claims above, or the recorded
fallback ("index declined — direct file reading", per the dispatcher
preflight). Design-phase grounding goes in design.md; notes.md keeps none.>

## Impact

<files/systems/dependencies touched; risks worth naming at open time>
```

## Rules

- `Preset:`/`Depends-on:` markers are machine-read (state rebuild) — keep
  them on their own lines, exactly as shown.
- Capability names must match existing `docs/specs/*.md` files or declare
  a new one.
- Breaking changes are marked at the bullet, not buried in prose.
