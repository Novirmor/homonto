package catalog

import (
	"strings"
	"testing"
)

// Anonymized regression cases from interrupted and completed PR continuations.
// These assert shipped prompt contracts, not model compliance or GitHub state.
func TestOntoDeliveryPolicyScenarios(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		want       []string
	}{
		{"skeptic returns executable repairs", "skills/onto/references/delivery.md", []string{
			"A skeptic report, failing test, unchecked final verification or recorded repair task is remaining work, not a stopping condition",
			"Execute the next action in the same invocation", "precise repair task", "owning full/fix/tweak route",
		}},
		{"missing evidence is actionable", "skills/onto-verify/references/adversarial.md", []string{
			"A summary or one-line pack is insufficient", "checking access", "Run the discriminating probe",
			"supported environments/scenarios", "exit statuses and full output",
		}},
		{"local-only scope and actual harm", "skills/onto-verify/references/adversarial.md", []string{
			"The coordinator owns scope and severity", "supported scenario, violated requirement",
			"arbitrary manual Helm overrides excluded by the contract is scope expansion",
			"Token shape alone cannot prove", "trace actual data flow",
			"startup importing an image it never pulled", "stale credentials after supported recovery",
			"recommending destructive purge for an unrelated failure", "never excuses proven credential exposure or data loss",
		}},
		{"bounded final candidate recheck", "skills/onto-verify/references/adversarial.md", []string{
			"Both mandatory lenses still cover the final candidate in full mode",
			"matching configured integration checks", "complete pack with finding IDs",
			"Do not reopen dismissed findings without new evidence or changed scope",
			"Rebind report receipts", "not a finding quota",
		}},
		{"missing living requirement before close", "skills/onto/references/delivery.md", []string{
			"before final verification", "configured records root", "Check exact requirement names",
			"`onto merge-deltas` has no dry-run flag and runs only at close",
			"investigate the configured root, history, capability name and operation",
			"Never invent a baseline or relabel MODIFIED merely to clear a gate",
		}},
		{"published continuation reaches requested endpoint", "skills/onto/references/delivery.md", []string{
			"intended open, non-draft PR", "required checks passing on its current head",
			"no confirmed in-scope blocking findings", "requested summary and feedback resolution",
			"Human approval and merge come afterward", "PR URLs, delivered head OIDs, observed check results",
		}},
		{"native done is not remote readiness", "skills/onto/references/delivery.md", []string{
			"`derived_phase: done` and a PR integration receipt prove native integration bookkeeping, not remote CI or review readiness",
			"an empty or unavailable status list is not proof that none are required",
			"If the head changes, reconcile", "Resume remote readiness checks even when native state is done",
		}},
		{"remote failure preserves archive", "skills/onto/references/delivery.md", []string{
			"source repair after archive needs a linked follow-up change", "normal selection/intent rules",
			"Never reopen or rewrite an immutable archive", "bounded timeout", "not success",
			"do not bypass checks or rerun blindly",
		}},
		{"explicit endpoint and authorization", "skills/onto/references/delivery.md", []string{
			"explicit verify-only endpoint does not acquire a PR requirement", "explicit pause",
			"concrete missing decision or permission", "publication authority and tool permissions",
			"A draft requested by the user remains draft", "do not retarget immutable bases",
		}},
		{"worker findings support parent action", "subagents/onto-skeptic.md", []string{
			"supported scenario", "violated requirement", "delivery impact", "stable ID",
			"expected distinguishing signal", "separate missing evidence from proven defects",
			"Do not reopen a dismissed finding without new evidence or changed scope",
		}},
		{"triage survives compaction and repair", "skills/onto-verify/references/verification.md", []string{
			"Finding ID", "Supported scenario / requirement", "Coordinator severity / disposition",
			"Next action / closure evidence", "confirmed pack consumption",
			"carry prior dispositions forward", "Finalize the report before recording artifact-bound receipts",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := hPromptText(t, tc.file)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("%s lacks delivery contract %q", tc.file, want)
				}
			}
		})
	}
}

func TestOntoEntriesLoadDeliveryLoop(t *testing.T) {
	for _, skill := range []string{"onto", "onto-open", "onto-design", "onto-build", "onto-verify", "onto-close", "onto-fix", "onto-tweak"} {
		text := hPromptText(t, "skills/"+skill+"/SKILL.md")
		if !strings.Contains(text, "references/delivery.md") {
			t.Errorf("%s direct entry does not load the delivery loop", skill)
		}
	}
	for _, file := range []string{"subagents/homonto.md", "skills/onto/references/recovery.md", "skills/onto-close/references/integration.md"} {
		if !strings.Contains(hPromptText(t, file), "delivery.md") {
			t.Errorf("%s loses the delivery loop", file)
		}
	}
	for _, skill := range []string{"onto", "onto-fix", "onto-tweak"} {
		text := hPromptText(t, "skills/"+skill+"/SKILL.md")
		if !strings.Contains(text, "| done |") || !strings.Contains(text, "delivery loop's endpoint check") {
			t.Errorf("%s must route native done to endpoint confirmation", skill)
		}
		if strings.Contains(text, "Report archived and integrated; new work requires its own selection") {
			t.Errorf("%s stops at native done", skill)
		}
	}
}
