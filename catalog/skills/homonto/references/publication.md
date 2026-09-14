# Verified source publication

Publication authorization does not expand the verified candidate. Apply this
contract to h resolve/continue and onto/to integration, including retries.

## Authority before execution

Default-allow shell execution is not automatic publishing permission. It permits
task-local execution, not a new external commitment. The coordinator alone owns
publication and authoritative GitHub intake; implementer Git/gh setup or reads
do not transfer that authority. Follow the invocation's exact publication scope
and the workflow's verification gates, even for publication inside a script,
interpreter, wrapper, or API payload. Known `git push`, GitHub publication, and
raw `gh api` patterns ask for the coordinator but are denied for implementers.
The coordinator auto-allows local Git operations; `git push` still asks. Implementers
retain prompts for destructive commands. These protected rules follow exact allow
additions, but cannot detect every hidden operation. A tool prompt cannot override role
ownership or publication approval.

Resolve authorizes its verified push and PR creation; continue authorizes its
verified push, summary comment, and demonstrably addressed thread resolutions.
Those actions need no second conversational approval. Review posting still
requires explicit approval of the shown draft; changed findings require renewed
approval. Other workflows use their own publication authorization, not h's by
analogy. An allowed command, a tool approval, or a private history of accepted
commands does not establish draft approval or expand the authorized scope.
Honor tool prompts and denials; never route around them through another tool.

## OpenCode comment and review drafts

When available, use `homonto_github_draft`, `homonto_github_status`, and
`homonto_github_publish` for issue comments, PR summary comments, and formal
`COMMENT` or `REQUEST_CHANGES` reviews. These tools belong to the coordinator;
workers cannot stage or publish. They do not push, create PRs, resolve threads,
or replace source verification. Existing resolve/continue authorization for
those other operations remains unchanged.

Stage a batch with `items`, each containing `kind` (`issue_comment`, `pr_comment`,
or `pr_review`), canonical HTTPS `url`, exact `body`, `baseOID`, `headOID`, and
`reviewEvent`. Issue comments use empty strings for the three review fields;
PR comments require reviewed OIDs and an empty event. The tool checks declared
repository scope and captures the posting account and destination. PR bodies
include the reviewed OIDs before preview. Read the returned full preview, not
the original body alone. Each body is limited to 8 KiB, each batch to ten items
and 32 KiB of bodies; do not silently truncate a larger draft.

Show the exact returned preview, then pass its `question` arguments unchanged
to OpenCode's native question tool. Each item offers Decline, Revise, or a unique
Publish choice in one dialog. Only the matching question reply approves that
item. A model-provided approval flag, tool permission, or earlier conversation
summary cannot approve it. If the user chooses Revise, stage a new batch and
show it again; staging invalidates unpublished approvals from the previous batch.
Publish unaffected approved items before staging revisions when appropriate.
No question tool or no reply means no publication.

Call `homonto_github_publish` with the returned `draftID` after the decision.
It sends only approved items, rechecks remote context after tool permission,
and returns per-item states and confirmed URLs. Stale items need a fresh review
and draft. Uncertain sends are reconcile-only: do not switch to shell posting,
stage another batch, or blindly retry. Repeating publish may confirm an existing
remote result but never resends an uncertain item. Declined items stay unpublished.

Drafts and approvals live only in the current plugin instance and session.
Restart requires restaging and fresh approval; do not infer approval from injected
recovery context. If publication may have been interrupted, reconcile the remote
destination first and stop if the outcome remains unclear. The native question
channel is workflow confirmation, not human-only attestation: host API clients
can answer questions, and trusted shell execution is not a sandbox.

If these tools are absent, use the skill's existing shown-draft approval and
body-file commands. A tool denial, stale draft, or uncertain outcome is not
tool absence. Formal `APPROVE` reviews remain on the existing commit-bound
review path, only when explicitly selected and with no critical or major findings.

## Pin and reconcile

For onto, read each source commit, target, mode, and receipt from the archived
integration record. Use the exact recorded verified candidate, never the source
branch's current tip beyond it. For to, read the final verification record's
candidate/tree OIDs and target from the plan/handoff; to has no onto receipt API.
An existing combined archive commit is eligible only when its ancestry contains
the verified source candidate and the complete intervening diff contains only
owned workflow bookkeeping. Name and inspect those commits. Unrelated source
commits after verification are not publication input. Separate/managed records
commits never become source candidates.

Before archive, identify each needed receiving checkout. Prefer preallocating a
registered receiver while the change is active, and record its alias, role,
target, identity, and absolute path in the handoff. After archive validate and
reuse that recorded path. Already archived recovery cannot go back in time:
when no receiver was allocated, use the identity-checked terminal receiver API
if the installed binary supports it. Otherwise report the missing safe receiver;
never recreate active state or make preallocation an impossible resume condition.
When multiple archive generations exist, select the evidence-matched native ID
or registered stateID with the receiver's `--state-id <recorded-state-id>` option;
the selector cannot rebind an existing worktree or override an identity conflict.

## Per-repo no-op first

After archive, before any merge or PR, compare each onto entry's recorded source and receiving
candidate. If it appears unchanged, use the binary's local proof first:

```sh
onto complete-integration <name> --receipt "unchanged:<receivingSHA>" --repo <alias> --dir "<configRoot>"
```

The receiving SHA must descend from the captured base and be on the recorded
target. At capture, the source must already be integrated or descend from that
base with an identical tree; the latter does not require a synthetic merge to
make the source commit reachable. The binary proves the no-op rather than
trusting an agent's diff summary. A changed source is not unchanged merely because
a later merge contains it. A
refusal needs investigation or the real merge/PR route, never a manufactured
empty merge or empty PR. This receipt is valid in either recorded integration
mode. Use no `--head` for unchanged or merge receipts. Repositories can finish
with different receipt kinds; all must be complete before declaring done. To
records a verified no-op in its own handoff, never through onto's receipt API.

Before pushing, inspect the receiving tree, status, diff, and candidate ancestry.
Reverify the actual integration tree after a merge, especially a changed target
or conflict resolution; to also needs a current completed skeptic verdict for a
changed tree. Pin the resulting verified delivery OID. Use an explicit non-force
refspec `git push "$REMOTE" "$DELIVERY_OID:refs/heads/$BRANCH"`, not a moving local
branch tip. Confirm the remote head OID after delivery. If the remote advanced,
reconcile without resetting, force-pushing, or publishing unverified additions.
Preserved dirt is not cleanup authorization or proof of integration safety.

## Exact PR identity

Carry canonical host and repository ID for both base and head repositories, head
owner, `headRefName`, `headRefOid`, target repository, and `baseRefName`. Normalize
SSH/HTTPS origins, then resolve requested and candidate repositories with
`gh repo view HOST/OWNER/REPO --json id,nameWithOwner,url`; compare canonical ID
and returned URL host, including forks, renames, and transfers. A reused slug is
not a match. Require an existing user-confirmed source-path-to-ID/host mapping or
ask once before local operations; two live URL lookups are not that anchor. Use
the canonical name without rewriting remotes. Every `gh --repo` includes
`HOST/OWNER/REPO`; API calls use `--hostname`. H workflows additionally retain the
full context-pack checks; this policy does not require h skills to be installed
for standalone onto/to integration.

List candidate PRs with pagination and enough metadata to compare these fields.
`--head "$BRANCH"` is only a search filter, never proof of an exact match: two
forks can have the same branch name. Fetch each candidate with `gh pr view`
including `state,url,headRepository,headRepositoryOwner,headRefName,headRefOid,baseRefName`;
resolve head repository canonical ID/host independently. Reuse exactly one OPEN
PR whose head repo ID/host, owner/ref, delivered head OID, base repo ID/host, and
target branch all match. Several exact matches are ambiguous; identity lookup
failure is unresolved, not no match. Create only after complete enumeration proves
no match, using an explicit fork-qualified head when required. Reconcile a lost
create response before retrying. Recheck OPEN and exact identity immediately before
any continuation work and again before each push. Closed/merged PRs are a blocker
for continuation, not permission to reopen or create a replacement automatically.

After independently observing the delivered PR head on that canonical remote,
record an explicit-source PR completion with all three scope/evidence flags:

```sh
onto complete-integration <name> --receipt "pr:<URL>" --head <observed-headOID> --repo <alias> --dir "<configRoot>"
```

`--head` is the publication tool's observed canonical full head OID, not merely
local HEAD or the desired delivery SHA. Recheck canonical head repository/host,
owner/ref, target and headOID before recording it, including reused PRs. Onto
checks local candidate ancestry, but stores PR URL/head as an external claim;
it does not verify remote delivery. Never fabricate that claim to satisfy the
flag. Legacy implicit config receipts omit `--repo` only; PR examples still use
the observed `--head` and `--dir`. Merge/unchanged receipts never take `--head`.

## Pending integration and feedback

An archived pending onto operation can only finish its recorded integration.
Merge mode merges the recorded candidate and records the real merge receipt.
PR mode may reuse an existing PR receipt only after the recorded source commits
were delivered to that exact canonical head and the PR targets the recorded base.
If a continuation's recorded target is the PR head but the existing PR targets
main, that is an integration-mode/target conflict, not a valid `pr:<URL>` receipt.
Report the conflict for explicit disposition; never change immutable state or
record an unrelated receipt merely to unblock it.

Completed-but-unpushed recovery is workflow-specific: onto reconciles its actual
integration receipt; to reconciles its archived plan, candidate, and integration
history without calling `onto state` or `onto complete-integration`. In either
case compare the freshly collected feedback against delivered commits. Finish
pending publication, but every remaining authorized feedback item still routes
through an active or new continuation change. Never shortcut past new feedback.
Discover matching to archives independently of whether local PR HEAD is ahead
of upstream. Match canonical PR host/repository ID/number plus source alias,
verified candidate/tree and recorded target evidence. This catches completed
work still on an isolated branch before local integration. Reconcile candidate
reachability in the receiver and remote: not locally integrated, integrated but
unpushed, and delivered are distinct recovery states. Resume the missing step
before creating another change, then route any remaining latest feedback to new
work; ambiguous archive generations or missing candidate evidence are blockers.

## Closing issues

The backend currently parses only the dedicated `Closes: #N` marker. It carries
no cross-repository identity. Record the issue's canonical host/repository ID and
URL alongside the change. Emit bare `Closes #N` only in a PR in that confirmed
issue origin repository. Suppress it in every other repository's PR or merge
message and retain the canonical URL as a non-closing reference instead. Do not
invent `Closes: OWNER/REPO#N` or URL marker syntax. If a backend-generated message
would emit the bare marker in another repo, omit the marker before archival and
report that automatic closing is unavailable; preserve the ordinary issue URL.
Without confirmed issue identity, suppress automatic closing rather than risk
closing a same-number issue in the wrong repository.

## Destination-specific bodies

The sanctioned archived `ship.md` is a neutral shared base body with no closing
directive. It may retain the canonical original issue URL as an ordinary link.
For every repository and every attempt, regenerate a separate body under session
tmp from that immutable base and the original issue identity. Add bare closing
text only after confirming this destination is the issue origin. Do not copy
an origin repo's rendered body into another repo or pass archived `ship.md`
directly as `--body-file`. If an older cached `ship.md` contains closing text,
leave it immutable, remove those directives in the temporary rendering, and
re-add only a verified origin-appropriate marker; never infer issue identity
from that cached marker alone. Missing identity means no closing directive.

Use a fresh per-repo tmp path keyed by safe canonical repository ID and attempt.
Record the destination canonical ID/host, target, observed headOID, absolute body
path and body hash in the session publication evidence; validate that tuple
before creating/reusing the PR and recording its receipt. Recheck on every retry,
never reuse a body solely because its filename exists. No `ship/<repo>.md`
archive directory or new archive-mutation exception is required. If a PR cannot
be opened, report the destination-specific temporary body path and pending
integration; neutral `ship.md` alone is not a ready-to-post closing body.
