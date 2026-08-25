# Host integration

Claude Code and OpenCode, on Linux and macOS.

```bash
homonto host install
homonto host install --tool claude --dry-run
```

Without `--tool`, Homonto installs for the tools it finds in use — the
ones whose project-local directory exists. That is a deliberately weak
signal and the right one: it does not install into a tool nobody uses
here, and does not refuse to install into one you have not opened yet.

## What gets installed

**Claude Code** (`.claude/`)

| Path | What |
|---|---|
| `skills/homonto-<workflow>/SKILL.md` | The one skill entry: the protocol loop |
| `commands/homonto-<workflow>.md` | A command that invokes the skill |
| `settings.json` | Two hooks: the resume probe and the write guard |

**OpenCode** (`.opencode/`)

| Path | What |
|---|---|
| `skill/homonto-<workflow>/SKILL.md` | The protocol loop |
| `command/homonto-<workflow>.md` | A command that invokes the skill |
| `plugin/homonto.js` | Event normalization: probe, guard, assignment tool |

Only the **configured** workflow's entry point is installed. A Task
workspace never grows a `/homonto-change` command: offering both would let
a host start work the workspace is not set up to run.

## Why they are thin

The generated files contain no workflow transitions, no required-artifact
rules, no routing policy, and no subagent prompts. A wrapper may invoke six
verbs — `next`, `report`, `decide`, `accept-edit`, `host probe`,
`host guard` — and a test asserts exactly that, reading actual invocations
rather than mentions.

The reason is not tidiness. Every rule in a wrapper is a rule that can
disagree with the binary: it is not versioned with it, not tested against
it, and not updated when it changes. A host that "knows" a workflow rule is
a host that will eventually be wrong about one, confidently, in a session
nobody is watching.

A second test refuses any mention of a workflow internal — a step name, a
required document — in the generated prose.

## Ownership

Every generated file carries a marker naming the digest of its own content:

```markdown
<!-- homonto-managed: sha256=… -->
```

That makes ownership a property of the file rather than of a state database
somewhere else. Homonto can tell a file it wrote from one you edited
without remembering what it wrote — and without being wrong after a
checkout, a merge, or a restore from backup.

| What Homonto finds | What it does |
|---|---|
| The marker matches the content | Its own; replaced when stale |
| The marker matches older content | Its own, from an older release; updated |
| The marker does not match | **You edited it.** Refused, not overwritten |
| No marker at all | Something else wrote it. Refused |

A plan carrying any conflict is refused **whole**. Installing the files
that happen not to conflict would leave wrappers and hooks disagreeing
about which Homonto they speak to.

`--adopt` replaces a file you edited. It is a deliberate flag and never a
default, because an edit is a statement.

## Shared documents

Claude's `settings.json` is yours as much as Homonto's, so it is projected
by managed key instead of owned outright. Only the hook entries that invoke
Homonto are rewritten; your model, your permissions, and your own hooks are
read, kept, and written back.

A `settings.json` Homonto cannot parse is **refused**, not rewritten.
Emitting a plan that replaces a document it failed to read would destroy
whatever was in there.

## Committing them

Generated files are project-local and gitignored by default. `--commit`
opts into committing them instead — useful when a team wants everyone on
the same integration, and a decision rather than a side effect.

## Drift

```bash
homonto doctor
```

reports files you edited, files missing, and files something else wrote. It
reports and never repairs: everything it finds is something you might have
done deliberately, and silently reverting it would be an argument rather
than a diagnosis.

## The resume probe

Fires on session start, reads state, writes nothing. It reports one
resumable work, the competing ones when there are several, or nothing.
It never chooses between two, and it tells the host so in as many words.

An idle workspace adds no session context at all: a session with no work in
progress should not begin with a paragraph about there being none.

## The assignment tool

OpenCode's plugin exposes `homonto_assignment`, which launches one
assignment as a child session. Its model, prompt, working directory, and
write scope all come from the **action**. Routing is configured per role
and per host in the manifest, resolved by the binary, and handed to the
host as data — a plugin that chose a model would be a plugin with opinions
about the workflow.
