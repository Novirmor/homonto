## Why

The `onto` and `to` frameworks name two specific third-party tools in shipped
skill prose — `rtk` (a token-optimizing shell proxy) and `graphify`
(code-intelligence grounding) — across 11 catalog files. ADR 0008 downgraded
them from hard requirements to warn-never-halt, but the names stayed baked in:
a user who does not run them is warned on every dispatch, and a user who
grounds with a different tool has no way to declare it. Which tools a
developer uses is configuration, not shipped prose.

## What Changes

- New neutral `[tooling]` config table with two managed keys:
  - `shell_proxy` — `"rtk"` or `"none"`
  - `code_intel` — `"graphify"`, `"okf"`, or `"none"`
- **BREAKING (shipped skill behavior, not config load)**: when `[tooling]` is
  absent both keys default to `"none"`. No third-party tool is named or probed
  unless the user opts in. Grounding falls back to direct file reading, which
  is already the documented fallback in ADR 0008. Existing configs keep
  loading unchanged; only the rendered skill text differs.
- `homonto apply` generates a per-install `references/tooling.md` inside the
  `onto` and `to` dispatcher skills from the declared providers. Catalog
  `SKILL.md` files stay byte-stable verbatim artifacts and defer to it,
  following ADR 0006's reference-file pattern.
- The 11 hardcoded `rtk`/`graphify` mentions become provider-neutral pointers.
- `okf` selects okf-generator as the code-intelligence provider. homonto
  references it and never vendors, downloads, or installs it — ADR 0015 bars
  third-party content from the catalog. Installing the provider stays the
  user's job, exactly as it already is for `rtk` and `graphify`.
- The catalog content fingerprint gains the resolved tooling config as an
  input, so editing `[tooling]` re-renders the sidecar instead of being
  swallowed by the materialize gate.

## Non-Goals

- Installing, downloading, updating, or version-checking any provider.
- A provider plugin API. The three code-intelligence values and two
  shell-proxy values are a closed set; adding a provider is a catalog change.
- Changing ADR 0008's warn-never-halt rule. A declared-but-absent provider
  still warns and proceeds; only the `onto` binary itself halts.
- Per-phase or per-skill provider overrides. One provider pair per repository.
- Retiring `rtk` or `graphify` as recommended tools; both remain first-class
  selectable values.

## Capabilities

### New Capabilities

- `tooling-providers`: declaring optional shell-proxy and code-intelligence
  providers in configuration, resolving their defaults, and rendering the
  selected pair into framework dispatcher skills at materialize time.

### Modified Capabilities

None. The two existing specs (`agent-models`, `onto-evidence-gates`) describe
model projection and onto's evidence gates; neither states a requirement about
tooling preflight, so no delta spec is needed.

## Impact

- `internal/config` — schema, validation of the closed value sets, defaults.
- `internal/catalog` — sidecar rendering and fingerprint inputs.
- `internal/engine` — threading the resolved tooling config into materialize.
- Catalog content — `skills/onto`, `skills/onto-open`, `skills/onto-design`,
  `skills/onto-close`, `commands/onto.md`, `subagents/onto-explorer.md`,
  `subagents/to-explorer.md`, and the `to` dispatcher.
- Docs — `guides/configuration.md`, `guides/onto-workflow.md`,
  `guides/to-workflow.md`, release notes, and a new ADR recording
  provider-neutrality.
- Catalog and framework version bumps; both frameworks re-materialize on the
  next apply.
