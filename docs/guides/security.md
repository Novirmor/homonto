# Security

What Homonto enforces, how, and — as importantly — what it does not claim.

## The write boundary has two halves

**The process gate.** `homonto guard` answers a cooperating host's write
hook. A session presents an operation and gets an allow or a refusal with a
reason and a machine-readable code.

**The final-diff gate.** Before a report is accepted or a workflow
advances, Homonto looks at what actually changed on disk and refuses
anything the assignment was not issued to change — including changes the
write hook never saw.

The second exists because the first can be bypassed. A shell command, a
child process, an editor plugin: any of them can write past a host hook.
**Homonto does not claim to prevent those writes. It refuses to build on
them.** An out-of-scope change invalidates the assignment and blocks
advancement, which is a weaker promise than a sandbox and an honest one.

## What the gate checks

Permission comes from exactly one of two places, and a request carries at
most one:

- an **assignment**, which lets an implementer write inside its issued
  isolation area and declared scope;
- an **edit grant**, which opens one workflow document once, in the phase
  the ownership table opened it in.

A session claiming neither has no permission to write. A read that writes
nothing needs no permission at all — refusing reads would only teach hosts
to stop asking.

Paths are checked widest-boundary-inward, so a refusal names the outermost
rule that was broken:

1. **Isolation area.** Outside the area the assignment was issued in.
2. **Protected state.** `.homonto/` — the runtime database, the checkpoint
   — is refused even to an assignment whose scope explicitly names it.
3. **Workflow documents.** Under `docs/homonto/`, writable only in the
   phase the ownership table gives them, and only to the party it names.
   A file called `proposal.md` in your source tree is source.
4. **Declared scope.**

Explorer, reviewer, and skeptic assignments write nothing. A stale,
unknown, invalidated, or already-answered assignment fails closed, as does
a consumed grant, a forged token, and anything malformed.

## Document ownership

| Phase | Who writes what |
|---|---|
| Task `plan` | You: the goal and the checklist |
| Task `do` | Homonto: checkboxes, for accepted assignments |
| Task `done` | Homonto: evidence |
| Change `open` | You: proposal, or fix/tweak plus tasks |
| Change `design` | You: design and tasks |
| Change `build` | You: plan. Homonto: checkboxes |
| Change `verify` | Homonto: `verification.md` |
| Change `close` | Homonto: `record.md`. An implementer: ADRs |

A frozen `preset-tasks.md` is editable by nobody.

Host edits go through single-use grants. A grant pins the document's
metadata and everything outside the granted regions, so accepting an edit
can prove nothing else moved. Grants are single-use, and a refused
acceptance leaves the grant open — nothing was accepted.

## Redaction

Raw subagent reports and command output stay in the local SQLite database.
What travels — a checkpoint, a committed record — carries counts, hashes,
commands, outcomes, decisions, and deviations, never content.

Check output is sanitized to valid UTF-8, has every forwarded environment
value replaced, and is bounded at 1 MiB per stream with truncation
recorded. Redaction is allowlist-based on the values a check was given, not
a guess at which variable names look sensitive.

The evidence records environment **names**, never values. A check that
echoes an allowlisted token back does not put it in the record.

## Untrusted input

Repository content, command output, Git metadata, archives, and host
reports are all data, never instructions. Structured protocol values are
schema-checked and strictly decoded; an unknown field is refused rather
than ignored.

## Command execution

Verification commands run as argument vectors — there is no shell, so there
is no injection — from a configured directory inside the member, under a
bounded timeout, in their own process group, with an environment built only
from the allowlisted names. A bare `argv[0]` is resolved against the
allowlisted `PATH` or refused; a command that silently borrows the parent's
`PATH` depends on something the evidence does not record.

A timed-out check has its whole process group killed, so a backgrounded
child cannot outlive it.

## Filesystem

Control-plane writes go through an fd-anchored layer that refuses symlinked
components, writes through unique temporary files with restrictive modes,
and fsyncs both file and directory before reporting success. A planted
symlink cannot redirect a control-plane access outside its anchor.

Exclusive creation is atomic in **content**, not just existence: a
concurrent reader sees either nothing or the whole file.

## Network

Ordinary Homonto processes make no network access. Homonto never calls a
model, provider, remote-content, or CI API — your host tool does that, in
its own process, over its own network.

Only `homonto update` reaches the network, only when you run it. See
[updates](updates.md).

## What is not claimed

- Not a sandbox, and not a complete security boundary.
- Not protection against a malicious host tool. A host that lies about what
  it did is caught by the final diff, but a host that is actively hostile
  has other options.
- Not protection against someone with write access to the control
  repository. The record is a record, not a vault.
