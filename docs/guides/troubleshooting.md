# Troubleshooting

Most of what Homonto tells you is a refusal, and most refusals are the
design working. This is how to read them.

## "no active work" / "N active works"

`homonto next` needs one unambiguous work. Name it:

```bash
homonto next fix-login
```

Homonto does not pick between two, because resuming the wrong work is worse
than asking. Finish or abandon the others if you want the short form back.

## A report was refused

```
task: assignment result rejected by the write boundary: guard: "internal/secret/keys.go"
was modified but assignment … was issued only src
```

Something changed a file outside the assignment's scope — usually a shell
command that walked past the write hook. **The report is refused, not
retried.** Fix the change (revert it, or re-issue with a scope that covers
it), then report again. The action stays answerable.

This is the half of the write boundary that does not depend on the host
cooperating, and it is doing its job.

## "no_permission"

The guard saw a write from a session claiming neither an assignment nor an
edit grant. Either the host did not pass the credentials back — check
`HOMONTO_ACTION_ID` and `HOMONTO_ACTION_TOKEN` in the subagent's
environment — or the write genuinely has no business happening.

## "protected_path"

Something tried to write `.homonto/`. That is Homonto's own state, refused
to every assignment regardless of scope. If an agent is trying to edit the
runtime database, the answer is not a wider scope.

## "wrong_phase" / "binary_owned"

A workflow document was written outside the phase that owns it, or in a
region only Homonto writes. Check the ownership table in
[security](security.md). Host edits go through an `edit` action's grant,
not through direct writes.

## An edit was refused

```
artifact: a region outside the grant changed
```

The grant opened specific regions. Something changed another region, the
metadata block, or the region markers. The grant is still open: put the
other regions back and accept again.

`artifact: document region structure is malformed` means the `<!-- homonto:begin … -->`
markers were damaged. Restore them; the layout is byte-precise on purpose,
so a grant's digests can partition the file exactly.

## A check will not run

```
verify: "go" is a bare command name but PATH is not allowlisted
```

The environment is an allowlist and nothing ambient gets through. Add what
the command needs:

```toml
environment = ["PATH", "HOME"]
```

This is stricter than most tools and it is deliberate: a check that
silently depends on your shell is a check whose evidence does not describe
what ran.

## A check times out

Its whole process group is killed, so a backgrounded child cannot outlive
it. Raise `timeout`, or find out why it hangs — a check that needs more
than ten minutes usually has a reason worth knowing.

## The workflow went backwards

Reconciliation. Something the step rested on moved:

```bash
homonto task status fix-login
```

prints the causes on stderr. Name the work: the bare `homonto task status`
reconciles every task and prints only where each one stands — the causes
go to the named form. The tables in the workflow guides say what each one
returns to. This is not a bug; it is the recorded step being checked
against the world rather than trusted.

If it keeps happening, something is editing a document behind the workflow
— a formatter, a hook, another agent.

## A preset keeps pausing

The scope assessment fired. The prompt lists exactly what fired and why.
The file count is a **warning**, not a verdict: it is asking, not refusing.

If it fires on files that should not count, your `[members.paths]` classes
are wrong — generated output and vendored code belong in them.

## Three failed repairs

Homonto stopped and asked. All three answers need a rationale. Accepting
the findings does **not** make a failing check pass; the checks run again
and if they still fail you will be asked again.

## "an interrupted self-update must be recovered first"

Run any command. Recovery happens at startup and either finishes or undoes
the activation.

If `homonto doctor` says the journal is unreadable, the update area is
damaged. Restore `.homonto/update/` from a backup, or remove the journal
to accept the installation as it stands — and check `homonto version`
afterwards to see which one you have.

## `homonto update` says it is unavailable

Your binary carries no signing root. That is expected for one you built
yourself. `homonto update trust` confirms it.

## A host file was refused

```
host: … exists and does not carry a matching homonto-managed marker
```

You edited a generated file. Homonto will not overwrite an edit. Either
keep it — nothing will touch it — or discard it with
`homonto host install --adopt`.

## "a work is already active in this workspace"

A workspace has exactly one active Task or Change. The refusal names the
one in the way; finish it or `homonto task abandon <name>` it.

This is not a capacity limit. Parallelism happens *inside* a work — a
round issues one assignment per unit, each in its own isolation area. Two
top-level works would share every member, so each one's checks would be
measuring a tree the other is also changing.

## "worktree is dirty" from an integration area

A new integration round starts from the base, and it will not reset an
area holding uncommitted changes: those are someone's unfinished conflict
resolution. Commit the resolution or discard it yourself, then re-run.

## "has uncommitted changes; commit, stash, or discard them first"

Work does not start over a dirty member. Homonto refuses rather than
tidying: deciding what to do with someone's uncommitted work is not its
call. The refusal names the member and the files.

The **control** repository is exempt — it holds the workflow documents
Homonto writes, so it is dirty as a matter of course.

The same refusal appears later if a member becomes dirty mid-work, since
an assignment cannot be cut from one.

## Nothing is happening

```bash
homonto doctor
homonto status
homonto next --json
```

in that order. `doctor` finds the structural problems, `status` shows where
everything is, and `next` says what it is waiting for.
