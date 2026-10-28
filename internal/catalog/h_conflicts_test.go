package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHResolveConflictsScopeAndDelivery(t *testing.T) {
	text := hPromptText(t, "skills/h-resolve-conflicts/SKILL.md")
	for _, want := range []string{
		"Do not require an engine", "Do not create lifecycle records",
		"OpenCode's built-in `question` tool", "every question to the user",
		"conflict-resolution-only", "pre-existing test hardening", "cumulative repair scope",
		"before editing beyond that boundary", "references/review-triage.md",
		"canonical host/repository IDs", "state: OPEN", "baseRefOid", "headRefOid",
		"engine-free checkout protocol", "Never automatically stash, reset, clean",
		"git merge-tree --write-tree", "No conflicts is a no-op", "not proof of no conflicts",
		"Before reporting a no-op, re-read both remote OIDs",
		`git merge --no-commit --no-ff "$BASE_OID"`, "both parents' intent",
		"Never blanket-select ours/theirs", "git diff --name-only --diff-filter=U -z",
		"h-spike", "h-review", "not an unrestricted PR audit",
		"two parents are the pinned head and base OIDs", "pre-existing failures separately",
		`git push "$HEAD_REMOTE" "$DELIVERY_OID:refs/heads/$HEAD_BRANCH"`,
		"If the base or head moved", "never force-push, rebase, amend",
		"Do not post comments, reviews or resolve threads", "mergeability is not CI readiness",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("conflict-only skill missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"gh pr merge", "gh pr update-branch", "git add -A", "git add .",
		"git push --force", "git checkout --ours", "git checkout --theirs",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("conflict skill suggests unsafe or out-of-scope operation %q", forbidden)
		}
	}
	command := hPromptText(t, "commands/h-resolve-conflicts.md")
	for _, want := range []string{"agent: homonto", "`h-resolve-conflicts` skill", "$ARGUMENTS", "conflict-only", "same invocation"} {
		if !strings.Contains(command, want) {
			t.Errorf("conflict command missing %q", want)
		}
	}
	for file, want := range map[string]string{
		"subagents/homonto.md":                          "h-resolve-conflicts",
		"skills/homonto/references/publication.md":      "conflict resolution authorizes only its verified push",
		"skills/homonto/references/workspace-policy.md": "h conflict resolution uses this same engine-free protocol",
		"subagents/h-review.md":                         "a complete **resolution packet** replaces the general PR-review packet",
		"subagents/h-spike.md":                          "no issue or engine-selection recommendation is required",
	} {
		if !strings.Contains(hPromptText(t, file), want) {
			t.Errorf("%s does not route conflict-only work: missing %q", file, want)
		}
	}
}

func TestHResolveConflictsMaterializesWithoutEngines(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := c.Expand([]string{"h"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	dir := t.TempDir()
	if err := c.Materialize(dir, names, "none", "none", "", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "h-resolve-conflicts", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "conflict-resolution-only") {
		t.Fatalf("conflict skill did not materialize: %v\n%s", err, data)
	}
	for _, engine := range []string{"onto", "to", "no"} {
		if _, err := os.Stat(filepath.Join(dir, engine)); !os.IsNotExist(err) {
			t.Fatalf("h unexpectedly installed engine %s: %v", engine, err)
		}
	}
}
