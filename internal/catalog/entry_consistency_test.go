package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Check complete entry paths, including negative contracts: a correct skill must
// not coexist with an older wrapper or shared policy that reverses its decision.
// These are shipped-prompt checks, not a claim about live model compliance.
func TestEntryPathPolicyConsistency(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  map[string][]string
		forbid []string
	}{
		{
			name: "h only continuation",
			files: map[string][]string{
				"commands/h-continue-pr.md":                     {"engine-optional", "user explicitly selects"},
				"skills/h-continue-pr/SKILL.md":                 {"Do not require an engine", "engine-free checkout protocol", "same PR"},
				"skills/h-resolve-issue/references/autonomy.md": {"Continuation is engine-optional", "New direct feedback work does not need a lifecycle record"},
				"skills/homonto/references/publication.md":      {"direct h continuation by default", "No archive, onto receipt or to plan is required"},
			},
			forbid: []string{"drive the matching to/onto change", "recommend viable `to` and onto paths", "feedback review (continue)", "every remaining authorized feedback item still routes through an active or new continuation change"},
		},
		{
			name: "no issue resolution",
			files: map[string][]string{
				"commands/h-resolve-issue.md":       {"record-free `no` route"},
				"skills/h-resolve-issue/SKILL.md":   {"../no/references/execution.md", "**no:** no archival or lifecycle receipt is required", "Common no/to PR publication", "instead of inventing a lifecycle record path"},
				"commands/no.md":                    {"references/execution.md"},
				"skills/no/SKILL.md":                {"references/execution.md"},
				"skills/no/references/execution.md": {"Never invoke onto/to", "parent owns commits", "first missing action", "never substitute for a configured failure"},
			},
			forbid: []string{"pick to or onto", "Do not patch the issue directly, however small"},
		},
		{
			name: "archived to delivery",
			files: map[string][]string{
				"commands/to.md":                   {"interrupted archived delivery"},
				"commands/to-done.md":              {"pending authorized integration/publication after archive"},
				"skills/to/SKILL.md":               {"references/recovery.md", "Route done archives to `to-done`"},
				"skills/to-done/SKILL.md":          {"For an archived done change", "steps 1–5 below apply only before archive", "../to/references/recovery.md"},
				"skills/to/references/recovery.md": {"lists active tasks only", "Integrated but not pushed", "An abandoned archive is unsuccessful history", "Do not rerun `to done`", "required checks passing on that head"},
			},
			forbid: []string{"only then report archived and route new work separately", "then finish with `to done"},
		},
		{
			name: "persistent user isolation",
			files: map[string][]string{
				"skills/homonto/references/autonomy.md":  {"Engine selection and task isolation are explicit user choices", "Reuse both recorded choices"},
				"skills/homonto/references/execution.md": {"Clean checkouts do not waive the choice", "per-repo exceptions"},
				"skills/onto-design/SKILL.md":            {"Reuse the user's task isolation choice", "do not choose a phase default"},
				"skills/onto-build/SKILL.md":             {"Reuse the recorded task choice", "ask once"},
				"skills/to-plan/SKILL.md":                {"Reuse the user's recorded task isolation"},
			},
			forbid: []string{"branch or worktree isolation, build mode", "For a clean serial change choose `branch`", "then choose worktree isolation"},
		},
		{
			name: "parent commits",
			files: map[string][]string{
				"skills/onto/SKILL.md":                   {"parent owns all commits"},
				"skills/homonto/references/execution.md": {"parent owns all source commits"},
				"skills/homonto/references/autonomy.md":  {"parent owns all implementation commits"},
				"subagents/onto-implementer.md":          {"Do not commit"},
				"subagents/to-implementer.md":            {"Do not commit"},
			},
			forbid: []string{"may commit only with explicit task authorization", "Implementation commits still require the task's explicit authorization"},
		},
		{
			name: "binary owned terminal cleanup",
			files: map[string][]string{
				"skills/homonto/references/execution.md": {"## Terminal cleanup", "cleanup list", "cleanup preview", "cleanup apply", "--identity", "native question dialog", "never relabeled complete", "archives and native branches remain", "Closing a change authorizes its cleanup: run it at close, without asking", "A PR delivery does not wait for the merge", "homonto tmp clean"},
				"subagents/homonto.md":                   {"execution.md#terminal-cleanup", "not manual deletion", "Run cleanup at close", "closing a change authorizes it, so do not ask"},
			},
		},
		{
			name: "all user questions use native dialogs",
			files: map[string][]string{
				"skills/homonto/references/autonomy.md": {"Every question to the user", "native dialog box", "clarifications, free-form questions", "even a single yes/no question", "unavailable, denied, or dismissed", "never infer consent or switch channels"},
				"subagents/homonto.md":                  {"every question to the user", "clarifications and free-form input", "native dialog box"},
				"skills/no/references/execution.md":     {"every question to the user", "OpenCode's built-in `question` tool", "native dialog box", "do not fall back to chat or shell prompts"},
				"skills/grilling/SKILL.md":              {"every question to the user", "clarifications and free-form input", "native dialog box", "standalone mode"},
				"skills/handoff/SKILL.md":               {"every question to the user", "clarifications and free-form input", "native dialog box", "standalone mode"},
			},
		},
		{
			name: "proportionate skeptic repairs",
			files: map[string][]string{
				"skills/homonto/references/review-triage.md":   {"## Proportionality before repair", "current user request", "conflict-resolution-only", "tightening otherwise unchanged PR tests", "cumulative repair scope", "before appending or dispatching repair tasks", "OpenCode's built-in `question` tool", "A blocking finding does not authorize its repair"},
				"skills/homonto/references/autonomy.md":        {"materially disproportionate", "review-triage.md#proportionality-before-repair", "Do not infer approval from a failing check"},
				"skills/onto-verify/SKILL.md":                  {"proportionality and scope approval", "before creating repair tasks", "For an authorized in-scope repair, ask"},
				"skills/to-done/SKILL.md":                      {"proportionality and scope approval", "before creating repair tasks", "current user request and assignment boundary"},
				"skills/onto-verify/references/adversarial.md": {"current user request and assignment boundary"},
				"skills/onto/references/delivery.md":           {"proportionality and scope approval", "pre-existing failures do not authorize repairs"},
				"skills/onto/references/recovery.md":           {"proportionality and scope approval", "before invalidating evidence or appending tasks"},
				"subagents/homonto.md":                         {"proportionality and scope approval", "before appending or dispatching repairs"},
				"subagents/onto-skeptic.md":                    {"current user request", "conflict-resolution-only", "test hardening", "scope approval"},
				"subagents/to-skeptic.md":                      {"current user request", "conflict-resolution-only", "test hardening", "scope approval"},
			},
			forbid: []string{"Ask the user only if accepting a known lower-severity deviation", "it asks the user only when product intent is genuinely missing"},
		},
		{
			name: "explorers before guessing",
			files: map[string][]string{
				"skills/homonto/references/execution.md":     {"## Focused exploration", "default to `onto-explorer` or `to-explorer`", "Before planning", "Before implementation", "During finding triage", "one bounded question per explorer", "Reuse grounded answers", "Trivial, already-grounded work may proceed directly", "do not replace review or final skepticism"},
				"skills/homonto/references/autonomy.md":      {"execution.md#focused-exploration"},
				"skills/homonto/references/review-triage.md": {"execution.md#focused-exploration", "introduced by the assigned change"},
				"skills/onto-design/SKILL.md":                {"execution.md#focused-exploration", "onto-explorer"},
				"skills/onto-build/SKILL.md":                 {"execution.md#focused-exploration", "before dispatching implementation"},
				"skills/to-plan/SKILL.md":                    {"execution.md#focused-exploration", "unfamiliar code"},
				"skills/to-do/SKILL.md":                      {"execution.md#focused-exploration", "before dispatching implementation"},
				"subagents/homonto.md":                       {"execution.md#focused-exploration", "not only after repeated failures"},
			},
		},
		{
			name: "bounded to skepticism",
			files: map[string][]string{
				"skills/to-done/SKILL.md":                      {"references/review-triage.md", "bounded repair recheck", "coordinator owns scope and severity"},
				"skills/to-do/SKILL.md":                        {"references/review-triage.md"},
				"skills/onto-verify/references/adversarial.md": {"references/review-triage.md"},
				"skills/homonto/references/review-triage.md":   {"Real data loss, credential exposure", "Do not reopen a dismissed finding without new evidence or changed scope"},
				"subagents/to-skeptic.md":                      {"stable ID", "separate missing evidence from proven defects", "Do not reopen a dismissed finding"},
			},
			forbid: []string{"Decline only findings refuted by evidence"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for file, required := range tc.files {
				text := hPromptText(t, file)
				for _, want := range required {
					if !strings.Contains(text, want) {
						t.Errorf("%s loses entry contract %q", file, want)
					}
				}
				for _, forbidden := range tc.forbid {
					if strings.Contains(text, forbidden) {
						t.Errorf("%s reverses entry policy with %q", file, forbidden)
					}
				}
			}
		})
	}
}

func TestStandaloneNoCoordinationReferenceMaterializes(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := c.Materialize(dir, []string{"no"}, "none", "none", "", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "no", "references", "execution.md"))
	if err != nil || !strings.Contains(string(data), "homonto workspace task check") {
		t.Fatalf("standalone no lacks executable parent contract: %v\n%s", err, data)
	}
	for _, absent := range []string{"homonto", "onto", "to"} {
		if _, err := os.Stat(filepath.Join(dir, absent)); !os.IsNotExist(err) {
			t.Fatalf("no unexpectedly acquired dependency %s: %v", absent, err)
		}
	}
	if _, ok := c.CommandPath("example-command"); ok {
		t.Fatal("placeholder command remains installable")
	}
}
