# Symlink owned content; merge unowned keys surgically; never clobber

- **Status:** Superseded by 0023
- **Date:** 2026-07-03
- **Change:** homonto-v1-core

## Context

Users author owned content in the repo (today under `homonto/`). The current
config model supports owned skills declared as explicit per-resource tables
(`[skills.<name>]` with `source` and `scope`); future roadmap items may add
commands, rules, or agents. Tools load content from their own directories, and
copying would create stale duplicates. Tool config files also contain keys
homonto does not manage, which must survive every apply.

## Decision

We will symlink supported owned content from `homonto/<kind>/<name>` (for
local-source resources) into each tool's directory, and never clobber: a
destination that exists and is not our symlink is reported as a conflict, not
overwritten. For JSON/JSONC config we write only managed keys and preserve every
unmanaged key's value on merge.

## Consequences

- Editing `homonto/...` is instantly live in every tool.
- Conflicts surface loudly and are resolved by humans.
- OpenCode JSONC comments are not preserved: any homonto write to
  `opencode.jsonc` standardizes the whole file as JSON and removes comments.
- Symlink targets must be absolute to be valid from anywhere (enforced
  since add-onto-workflow).
