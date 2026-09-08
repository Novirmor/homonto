package catalog

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

// These are embedded prompt contracts, not runtime proof of fetches or worker behavior.
func TestHReviewPaginationAuditContracts(t *testing.T) {
	pack := hPromptText(t, "skills/h-review-pr/references/context-pack.md")
	for _, want := range []string{"legacy schemas 0/1 also include the implicit config source", "messageHeadline messageBody authoredDate", "complete message bodies on every page"} {
		if !strings.Contains(pack, want) {
			t.Errorf("context pack missing %q", want)
		}
	}
	batch := hPromptText(t, "skills/h-review-batch/SKILL.md")
	if !strings.Contains(batch, "`--author \"$AUTHOR\"` in the server query before applying `--limit N`") {
		t.Error("batch must filter authors before limiting the result")
	}
}

func TestHReviewContextContracts(t *testing.T) {
	const pack = "skills/h-review-pr/references/context-pack.md"
	for _, tc := range []struct {
		name string
		file string
		want []string
	}{
		{
			"materialized_handoff", pack,
			[]string{
				"materialize a separate, session-local pack directory for each PR",
				"scratch directory readable by the worker under its existing permissions",
				"Write `manifest.json` listing the canonical repository ID/URL, source alias and absolute source path, PR number, base/head OIDs",
				"exact absolute paths to:",
				"`metadata.json`: description, reviews, comments, requests, and pinned refs",
				"`diff.patch`: the complete verified diff",
				"`review-threads.json`: every thread and its fully paginated comments",
				"`commits.json` and `files.json`: complete inventories, not the first page",
				"`linked-issues.json`: fetched issue bodies and relevant comments",
				"`checks.json`: check results from `statusCheckRollup`",
				"`git-context.txt`, when needed: coordinator-run commit/file inspection with the exact command, pinned revision, exit status, and full output",
				"An empty collection must be explicitly present as `[]` with a completed-fetch status; missing, inaccessible, truncated, or failed data is not an empty result",
			},
		},
		{
			"ready_snapshot", pack,
			[]string{
				"Pin both `baseRefOid` and `headRefOid` in the pack. Re-read those fields after fetching the diff and before dispatch. If either changed, discard the mixed snapshot and rebuild",
				"Reopen each staged file, validate JSON and required fields, confirm the pinned refs and complete file inventory, then mark the manifest `ready`",
				"A partially written or mixed-head pack is never ready",
				"The task prompt must include the absolute manifest path, every required file path, canonical PR identity, pinned OIDs, source directory, review lens, and the instruction to read the pack before analysis",
				"a summary or a reference to the coordinator's earlier tool output is not",
				"pass the same immutable ready snapshot to each",
				"The worker must confirm it can read the manifest and all required components before reviewing",
				"Do not mark a PR reviewed merely because a worker returned; validate both pack consumption and its citations",
			},
		},
		{
			"worker_evidence_boundary", "subagents/h-review.md",
			[]string{
				"read_only: true bash: false network: true",
				"Read the supplied manifest and every required component (or the complete inline packet)",
				"Confirm the canonical PR identity and base/head OIDs",
				"components return `Questions:` with their exact paths; do not review a partial pack or report \"no findings\"",
				"Begin the report with **Context used**: manifest path or inline packet identity, pinned refs, and components read",
				"Do not attempt `git show`, `git diff`, `gh`, or test commands",
				"`Questions:`; the coordinator executes it and supplies full output",
				"Do not request wider permissions or evade this division of work through web tools",
				"Use webfetch/websearch for supporting research, not GitHub operations",
			},
		},
		{
			"coordinator_probes", pack,
			[]string{
				"Never assign it `git show`, `git diff`, `gh`, tests, or other terminal commands",
				"The coordinator runs necessary probes against pinned commits, attaches their full output, and re-dispatches",
				"an evidence request, not an instruction to loosen its permissions",
				"Local file reads are supporting context only when the file is known to match the pinned revision",
			},
		},
		{
			"canonical_repository_identity", pack,
			[]string{
				"do not reject a different slug before checking for a rename or transfer",
				"resolves both the requested repository and each candidate declared source's origin through GitHub",
				"gh repo view HOST/OWNER/REPO --json id,nameWithOwner,url",
				"API requests use `--hostname HOST`",
				"Validate the returned URL's host against the requested host",
				"Compare the canonical repository `id` and host, not just names or URL spelling",
				"A reused old slug resolving to a different ID is not a match",
				"Exactly one verified match is usable; several matches require disambiguation, never the first path",
				"A failed identity lookup is unresolved identity, not proof of a mismatch",
				"A mismatch is a blocker only after canonical identity verification",
				"Two live lookups of the same mutable origin URL are not an independent checkout identity anchor",
				"prior user-confirmed mapping of this source path to the canonical ID/host, or ask once",
				"a complete remote-only pack can still be reviewed",
			},
		},
		{
			"bounded_diff_recovery", pack,
			[]string{
				"HTTP 500/502/503/504, transport timeouts",
				"\"diff temporarily unavailable due to heavy server load\"",
				"at most three attempts total per PR in this invocation, waiting 2 seconds then 5 seconds",
				"honor Retry-After only within a 30-second retry budget; otherwise defer and report when to retry",
				"Do not retry authentication failures or denied tool permissions as transient server errors",
				"Only exhausted transient failures qualify for a fallback; authentication or permission failures do not authorize another fetch route",
				"A failed download's partial output is not a diff",
				"local fallback only in the identity-verified source repository with both pinned commit objects available",
				"git --no-replace-objects -C \"$SOURCE_DIR\" diff --no-ext-diff --no-textconv --binary --full-index \"$BASE_OID...$HEAD_OID\" --",
				"Use `--no-replace-objects` for every pinned object check, merge-base resolution, diff, and supporting Git probe",
				"Reject legacy grafts or an incomplete/shallow history",
				"Record the local provenance, merge-base OID, and command in the manifest, and reconcile its changed-file inventory with the fully fetched PR file list",
				"If the objects, ref recheck, or inventory cannot be validated, do not mark the fallback complete",
				"An unexplained empty diff is incomplete too",
			},
		},
		{
			"publication_freshness", pack,
			[]string{
				"Immediately before each approved post, revalidate canonical repository ID/host, PR number/state, and base/head OIDs",
				"Changed refs invalidate the draft: rebuild the pack, re-review, and obtain fresh approval",
				"Every posted body identifies the reviewed base/head OIDs",
				"bind GitHub's `commit_id` to the reviewed head OID",
				"gh api --hostname \"$HOST\" --method POST \"repos/$OWNER/$REPO/pulls/$NUMBER/reviews\" --input \"$REVIEW_FILE\"",
				"Confirm the returned review's `commit_id` and URL",
				"Reconcile a lost response against existing reviews before retrying a mutation",
			},
		},
		{
			"single_review_dispatch", "skills/h-review-pr/SKILL.md",
			[]string{
				"use the shared bounded retry and pinned local-diff fallback",
				"absolute manifest and component paths, pinned refs, source path, and review lens, not a summary or a pointer to earlier tool output",
				"Dispatch only a ready pack",
				"Require the worker's Context used report to identify the pack it actually read",
				"A missing-context return is not a completed review",
			},
		},
		{
			"batch_deferred_vs_reviewed", "skills/h-review-batch/SKILL.md",
			[]string{
				"defer HTTP 5xx diffs while other PRs proceed, retry within the same three-attempt budget",
				"pinned local-diff fallback only if its identity, objects, and inventory can be verified",
				"marked failed and skipped; the others proceed",
				"complete pack's absolute manifest/component paths and pinned refs",
				"Only ready packs enter a wave; earlier coordinator tool output is not a handoff",
				"require the worker's Context used report",
				"Include attempts and fallback provenance; retain retryable failures with the exact next action",
				"Never count a Questions-only return as reviewed",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := hPromptText(t, tc.file)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("%s missing prompt contract %q", tc.file, want)
				}
			}
		})
	}
}

func TestHReviewContextSharedReferences(t *testing.T) {
	for _, tc := range []struct {
		file string
		ref  string
	}{
		{"skills/h-review-pr/SKILL.md", "references/context-pack.md"},
		{"skills/h-review-batch/SKILL.md", "../h-review-pr/references/context-pack.md"},
		{"skills/h-spike-issue/SKILL.md", "../h-review-pr/references/context-pack.md#repository-identity"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if !strings.Contains(hPromptText(t, tc.file), "]("+tc.ref+")") {
				t.Fatalf("missing shared context reference %q", tc.ref)
			}
			file, anchor, _ := strings.Cut(tc.ref, "#")
			resolved := path.Join(path.Dir(tc.file), file)
			if resolved != "skills/h-review-pr/references/context-pack.md" {
				t.Fatalf("reference resolves to %q, not the shared context pack", resolved)
			}
			content, err := fs.ReadFile(embedded.FS, resolved)
			if err != nil {
				t.Fatal(err)
			}
			if anchor != "" {
				for _, line := range strings.Split(string(content), "\n") {
					if heading, ok := strings.CutPrefix(line, "## "); ok && strings.ReplaceAll(strings.ToLower(heading), " ", "-") == anchor {
						return
					}
				}
				t.Errorf("%s has no heading for anchor %q", resolved, anchor)
			}
		})
	}
}
