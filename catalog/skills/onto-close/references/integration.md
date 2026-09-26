# Source integration after archive

Read for onto-close step 4 or archived pending recovery. Follow
[verified source publication](../../homonto/references/publication.md) for exact
candidate pinning, canonical remote identity, approval, freshness and retry rules,
and [workspace policy](../../homonto/references/workspace-policy.md) for receivers.

## Read the recorded candidate

Read each source commit, target branch and mode from archived `.onto/integration.json`.
Schema 2 has no implicit config entry. With separate/managed records, integrate the
exact recorded verified candidate; never use a records archive/checkpoint SHA as source.
An existing combined checkout may use a pinned archive descendant only after
proving its complete intervening diff is owned records-only bookkeeping. Never
use the source branch's moving tip beyond that evidence.

First route apparently unchanged sources through:

```sh
onto complete-integration <name> --receipt "unchanged:<receivingSHA>" --repo <alias> --dir "<configRoot>"
```

The binary proves unchanged against recorded source/target. A refusal needs
investigation or actual delivery, not an empty PR/merge. Do not pass `--head` for
unchanged or merge. Each source completes independently.

## Merge route

Use the authorized clean target checkout or validate and reuse the recorded
receiver. Branch isolation can check out the recorded target in its safe root;
worktree isolation uses the checkout already holding that branch. Never switch
a dirty original or force a target free. If terminal recovery lacks a receiver,
use supported `homonto worktree receiver <name> --workflow onto --repo <alias> --json`
with the matched archive identity; an unsupported API/occupied target is a blocker.

```sh
git merge --no-ff <sourceCommit>
```

Resolve mechanical conflicts from evidence; abort and use the built-in `question`
tool when resolution needs product intent. Verify the actual receiving tree under
the publication contract before push. Record the successful merge:

```sh
onto complete-integration <name> --receipt "merge:<merge-commit>" --repo <alias> --dir "<configRoot>"
```

The binary requires a real no-ff merge containing the recorded candidate and
reachable from the target. Do not substitute a PR URL for a continuation's merge.

## PR route

Use [ship-handoff.md](ship-handoff.md) for the neutral shared body. Add only a
neutral `ship.md` to the archive, preserving an already committed copy. Record
that sanctioned addition in the records owner (managed named checkpoint or
existing named commit). No new `ship/` subtree is authorized.

For each destination and retry, render a fresh session-tmp body under the shared
publication contract. A closing marker belongs only to the confirmed issue origin;
never pass neutral `ship.md` directly as an origin-specific body.

Pin the verified delivery OID; read remote/ref names into shell parameters and
quote them. Publish only within the invocation's authorization:

```sh
git push "$REMOTE" "$DELIVERY_OID:refs/heads/$CHANGE_BRANCH"
gh pr list --repo HOST/OWNER/REPO --head "$CHANGE_BRANCH" --base "$BASE_BRANCH" --state open --json number,url
```

Paginate; list filters are candidates only. Independently fetch canonical head/base
repository IDs and host, head owner/ref/OID and target. Reuse one exact OPEN match
after delivery. Several matches require a decision; lookup failure is not no match.
Create only after complete enumeration proves absence, using fork-qualified head
when required and quoted metadata rather than interpolated issue text:

```sh
gh pr create --repo HOST/OWNER/REPO --head "$QUALIFIED_HEAD" --base "$BASE_BRANCH" --fill --body-file "$REPO_BODY_FILE"
```

Confirm remote head and target, then record:

```sh
onto complete-integration <name> --receipt "pr:<https-url>" --head <observed-headOID> --repo <alias> --dir "<configRoot>"
```

`--head` is the observed remote OID, not guessed local HEAD. Onto stores an external
claim, not proof of GitHub delivery. Review/merge on the hosting platform remain
outside onto. Missing gh/remote or denied/unresolved publication leaves integration
pending; report the destination-specific body path and blocker, never claim done.

## Record and clean up

Managed receipt writes checkpoint automatically; existing mode commits the sidecar
in its records owner and pushes it only when applicable and authorized. Legacy
combined mode omits `--repo` for its implicit config receipt, plus records each
selected sibling separately. Receipt completion is one-way and idempotent.

After terminal integration and authorized cleanup, use registered
`homonto worktree remove <name> --workflow onto --repo <alias> --yes`.
A newly opened PR can still leave a source unintegrated for removal: retain it on
refusal. Legacy raw worktrees use
[worktree-protocol.md](../../onto-build/references/worktree-protocol.md)'s exact-path
clean/integrated teardown. Never force removal, prune around refusal or delete branches.
