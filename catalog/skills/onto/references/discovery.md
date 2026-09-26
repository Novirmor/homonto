# Onto discovery

Read this at dispatcher discovery. Follow the shared
[workspace policy](../../homonto/references/workspace-policy.md) for roots,
dirty-work choices, initialization and isolation.

## Inventory and identity

Inspect both workflow inventories. An onto change is active when its directory
is directly under `<workflow-root>/changes/`, contains `proposal.md` or
`onto-state.yaml`, and its state is not archived or abandoned. Ignore scratch and
template directories; never reconstruct state in a directory with neither artifact.

Also inspect archives for `archived: false` (interrupted close) and pending or
malformed `.onto/integration.json`. Resume those at close; archival alone is not
completion. `onto close <name>` completes the interrupted directory move/flag.
Pending integration finishes through `onto complete-integration`, never new work
inside the archive.

| Inventory / request | Route |
|---|---|
| No match, new work requested | Shared workflow selection, then selected entry skill |
| No match, no work described | Ask what to work on through the built-in `question` tool |
| Unique matching active change | Resume its recorded workflow |
| One active change, request fits scope | Continue it |
| Named or uniquely matching change among several | Resume that change |
| Several plausible identities or independent/conflicting scope | Ask the concrete identity/scope question |

Names are globally unique across onto/to. Inspect identity, not just a name:
a name collision with unrelated work needs a fresh name; a genuine conversion
uses promote/demote and respects active-binding restrictions. Never duplicate or
silently convert an existing change to satisfy a new command.

## Dependencies and dirt

Run `onto dirt <name> --json` before writes and follow
[dirty-workspace.md](dirty-workspace.md). Attribute source dirt; preserve or isolate
under the existing decision, or ask once for the exact uncovered paths.

Dependencies resolve through exact `YYYY-MM-DD-<name>` archives: legacy archives
resolve directly; tracked archives require completed integration. A pending or
malformed receipt does not resolve a dependency. An active same-name generation
overrides an archive hit. Show ready/blocked status in discovery.

Do not build while dependencies are unresolved. Route to a unique active next
dependency when authorized. Missing dependency identities and cycles (including
self-dependencies) need a user decision; multiple choices need one only when
priority is unclear. Do not drop a dependency or waive its order automatically.
Close changes serially because specs and ADR numbering share the records root.

## First-use setup

In managed history mode, obtain explicit initialization authorization and run
`homonto workspace init --yes` while the records root is empty, before writing
directories or README files. Never initialize config/source Git as repair.

If `<workflow-root>/changes/` is absent, create
`<workflow-root>/{adr,specs,changes/archive,guides}/` after initialization. Use
`references/changes-readme.md` and `onto-close/references/specs-readme.md` for the
changes/specs READMEs. Then route to the chosen open/full or preset skill.
