# Recover Or Transfer Work

Use normal workspace commands after an interruption. A read-write workspace
open finishes or rolls back pending Homonto operations before it starts new
work.

## Resume After A Crash

Run a command that opens the workspace for writing, such as:

```bash
homonto status
```

Read-only commands, including `version` and host probes, do not run workspace
recovery. Use `doctor` when the workspace opens but reports a member,
integration, active-work, or update condition:

```bash
homonto doctor
```

Recovery handles interrupted operations. It does not preserve stale evidence
after a document, configuration, or member changes. See [Workflows](../concepts/workflows.md)
for invalidation.

## Handoff To Another Machine

On the machine that holds the active work:

```bash
homonto handoff
git push
```

`handoff` commits a portable checkpoint and releases the local leases. Clone or
update the control repository on the destination machine, then inspect proposed
member mappings:

```bash
git clone <control-repository>
cd <control-repository>
homonto attach --propose
```

Confirm each member path rather than accepting a guess:

```bash
homonto attach --member <repository-id>=/path/to/member
```

Attach rebuilds local runtime state and resumes from the start of the stored
phase. A handoff checkpoint has one normal attach hop.

## Force A Takeover

Use a forced attach only when the normal handoff cannot complete:

```bash
homonto attach --force --member <repository-id>=/path/to/member
```

Forced takeover advances the execution generation and makes recorded evidence
stale. Homonto requires the affected work to establish fresh evidence before it
advances.

## Resolve Ambiguous Work

Homonto normally allows one active top-level work. A legacy or manually repaired
workspace can contain ambiguous active state. The host probe and `next` refuse
to choose between works. Use `status` and `doctor` to identify the records, then
finish or abandon the work that should not remain active.
