# Updates

`homonto update` is the only command that touches the network. It runs only
when you type it. Homonto never checks for updates on its own.

```bash
homonto update trust
```

tells you whether it is even available. A binary you built yourself carries
no signing root, verifies nothing, and refuses to update. That is the safe
default: a build that could replace itself from the network without a
trusted signature would be worse than one that cannot update at all.

## What happens

1. **Fetch the manifest.** HTTPS only, TLS 1.2 or better, bounded, timed
   out, and **redirects are refused**. A redirect moves the fetch to a host
   nobody vetted, and "the signature still checks out" is not an argument
   for following one — pinning a URL is how the set of machines involved is
   known in advance.
2. **Verify the signature** against the roots compiled into your binary.
   Signatures cover the canonical manifest with the signatures removed,
   because a signature cannot cover itself. The same root signing twice is
   one signature; a threshold cannot be met by repetition. A signature from
   a root your binary does not know is refused, not skipped.
3. **Check the channel.** It is inside the signed document, so a beta
   manifest served at the stable address is refused even though its
   signature is perfectly valid. The signature attests to what a manifest
   says, not to where it was found.
4. **Fetch and checksum the artifact.**
5. **Stage** it aside and **interrogate** it: run the candidate with an
   empty environment in a temporary directory and ask what it is. The
   manifest describes what a release is supposed to be; this is the only
   way to learn what it actually is.
6. **Check compatibility.** Refused if it is not newer, if its protocol
   goes backwards, or if its store schema does. Each strands a workspace
   differently, and the schema case is the nastiest: the migration ledger
   correctly refuses a database recorded newer than the binary knows.
7. **Refuse while work is active.** Replacing the binary under a running
   workflow means the next `homonto next` is answered by a different
   program than the one that issued the outstanding actions.
8. **Activate**, under a journal.

## Activation

Each component is replaced by one atomic per-file operation with its exact
prior bytes preserved first, and the journal records each transition before
and after it happens. The order is fixed and the **activated-generation
marker is written last**.

That marker is the single bit that says which installation this machine is
running. A crash before it leaves an update that rolls back; a crash after
it leaves one that finishes forward.

No cross-filesystem atomic transaction is claimed. What is claimed is
narrower and true: every individual replacement is atomic, every replaced
file has an exact backup, and the marker distinguishes finished from in
progress.

## Recovery

The next invocation — of either binary; the journal format is deliberately
readable by both — finishes or undoes the interrupted activation before
anything else runs.

Rollback reverts in **reverse** order, marker first and binary last. A
crash mid-rollback must never leave a machine with the new marker and the
old binary, which would tell the next invocation that an activation it
never finished had succeeded.

A backup that cannot be restored is reported loudly and the journal is
**left in place**. There is no good answer to a failed restore, and the
worst one would be to clear the record and let ordinary commands resume
against an installation nobody can describe. `homonto doctor` will keep
telling you.

## Backups

Exact pre-update copies are retained under `.homonto/update/backup/`. They
are what makes a later manual restore possible, so they are not cleaned up
when an update succeeds.

## Signing-key rotation

A manifest may carry the next set of signing roots. It is accepted only
when the manifest itself is signed by roots your binary already trusts,
**and** the new set retains at least the threshold number of them, **and**
the candidate binary actually carries them.

A release can retire one key and introduce another. It cannot replace the
whole set in one step, because a manifest that did would be
indistinguishable from a manifest that had captured the update channel.

## Migrations

A migration without a tested reverse or an exact backup restore path is not
eligible for self-update. Schema changes are tested against copies of the
database and the checkpoint before activation, not against the originals.
