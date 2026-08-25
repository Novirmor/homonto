# Tooling providers are declared in config, not named in shipped prose

- **Status:** Superseded by 0023
- **Date:** 2026-07-26

*(Written 2026-07-27, after the fact. The decision shipped in the
`optional-tooling-providers` change and was announced in the v0.10.0 release
notes, which linked to an ADR nobody had written; the number cited there,
0017, had meanwhile been taken by
[stop-committing-workflow-artifacts](0017-stop-committing-workflow-artifacts.md).
This file is that missing record, at the next free number.)*

## Context

The `onto` and `to` skills named specific third-party tools in their shipped
prose: `rtk` as a shell proxy and `graphify` as the code-intelligence index.
Every repository that installed a framework therefore got a preflight
instructing the agent to run tools the repository might not have, might not
want, and that homonto has never installed.

That coupling had three costs. Adopters without those tools read a preflight
that named commands they could not run. Adopters with a *different* index had
no way to say so. And homonto — whose whole contract is projecting config into
tools it does not own — was shipping opinions about a toolchain in the one
place a user could not override: the skill text itself.

## Decision

Which providers the workflow grounds against is **configuration**, declared in
a `[tooling]` table, and the shipped skills name none of them:

```toml
[tooling]
shell_proxy = "rtk"       # "rtk" | "none"
code_intel  = "graphify"  # "graphify" | "okf" | "none"
```

- Both keys **default to `none`**. A config with no `[tooling]` table gets a
  preflight naming no tool, and grounding falls back to direct file reading —
  which was already the documented fallback.
- `homonto apply` **generates `references/tooling.md`** inside each framework's
  dispatcher skill, describing exactly the declared pair. Shipped `SKILL.md`
  files defer to that file, so a provider you did not declare is never
  mentioned. It is regenerated on every apply and must not be hand-edited.
- The provider sets are **closed and validated at load**. An unknown key or
  value fails naming the offender and the accepted set. There is no plugin API
  for providers, by design: adding one is a deliberate change to
  `internal/config/tooling.go` plus a matching `catalog/tooling/<name>.md`
  fragment.
- homonto **never installs, updates, or runs** a provider. `homonto doctor`
  probes `PATH` and index directories to warn when a declared provider is not
  detected, and never executes it.
- A declared-but-missing provider **warns and proceeds**. A degraded session
  still works; the workflow never halts on a missing grounding tool
  ([ADR 0008](0008-preflight-warns-not-halts.md)).

## Consequences

- Breaking for the *rendered* workflow, not for configs: existing
  `homonto.toml` files keep loading unchanged, but a repo that relied on the
  old hardcoded prose must add the two lines to keep naming its tools.
- Adding a provider is a code change plus a catalog fragment, not a config
  entry. That is the intended friction — a closed set is what lets validation
  reject a typo by name instead of silently accepting it.
- The skills lost their ability to assume any particular grounding tool, which
  is why every phase skill states that direct file reading is always an
  acceptable fallback and that the session should say which grounding it used.
- homonto still ships an opinion about *whether* to ground, just not about
  *with what*.
