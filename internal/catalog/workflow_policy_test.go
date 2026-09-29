package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These scenarios check conflicting shipped instructions, not model compliance.
// Executable preset recovery is covered by TestWorkflowPromptCLISequences.
func TestWorkflowPolicyScenarios(t *testing.T) {
	for _, tc := range []struct {
		name, file   string
		want, forbid []string
	}{
		{"explicit full choice for small bug", "skills/onto-open/SKILL.md",
			[]string{"full-onto choice stays full", "Never redirect selected full work to a preset"},
			[]string{"if the request fits a preset, hand over"}},
		{"defect after passing verification", "skills/onto/references/recovery.md",
			[]string{"autonomous repair even after verification passed", "No new consent is needed", "Result: superseded", "verify-result <name> pending", "Keep the recorded phase"},
			[]string{"both need explicit user intent"}},
		{"preset verify repair", "skills/onto-verify/SKILL.md",
			[]string{"| fix |", "`onto-fix` step 2", "| tweak |", "`onto-tweak` step 2", "Do not create a plan or design for a preset repair"}, nil},
		{"preset direct build entry", "skills/onto-build/SKILL.md",
			[]string{"return to `onto-fix` or `onto-tweak` step 2", "no new design or plan requirement"},
			[]string{"~200 lines"}},
		{"unavailable to skeptic", "skills/to-done/SKILL.md",
			[]string{"dispatch or the worker is unavailable", "perform the claim/gap checks directly", "Explicit independent-review requirements still block", "Never label direct review a completed skeptic pass"}, nil},
		{"denial is not unavailable dispatch", "skills/homonto/references/execution.md",
			[]string{"Tool permission denied", "no direct execution or alternate tool as a workaround", "an unsuccessful attempt is not absence", "explicitly requires independent review", "record the dependency and next executable task"}, nil},
		{"unchanged evidence across phases", "skills/homonto/references/execution.md",
			[]string{"may satisfy multiple checkpoints", "unchanged", "coverage matches the claim", "uncertain provenance requires a rerun", "merge/conflict resolution invalidates"}, nil},
		{"configuration and test-count eligibility", "skills/homonto/references/workflow-selection.md",
			[]string{"Test count alone never forces escalation", "private implementation detail need not", "new public contract or migration requires full onto", "Informational questions"},
			[]string{"5+ new test cases"}},
		{"tweak entry does not promise light verification", "skills/onto-tweak/SKILL.md",
			[]string{"explicitly selected", "active tweak change", "workflow-selection.md#eligibility-and-escalation", "onto-verify`'s scale and risk check"},
			[]string{"≤5 files", "light verify"}},
		{"tweak command defers eligibility and risk", "commands/onto-tweak.md",
			[]string{"eligibility", "verification scale", "`onto-tweak` skill"},
			[]string{"≤5 files", "light verify"}},
		{"two source files and six tests", "skills/homonto/references/workflow-selection.md",
			[]string{"at most five non-test files", "excluding tests and workflow bookkeeping", "Test count alone never forces escalation"}, nil},
		{"one security-sensitive file", "skills/homonto/references/workflow-selection.md",
			[]string{"security-sensitive surface needs full verification even if its preset still fits"}, nil},
		{"intermittent and multi-component failures", "skills/homonto/references/debugging.md",
			[]string{"If reproduction is unreliable", "gather evidence", "inputs, outputs", "without exposing secrets", "working path", "one variable at a time", "3 failed hypotheses", "outside the assigned scope"}, nil},
		{"native decision channel", "skills/homonto/references/autonomy.md",
			[]string{"OpenCode's built-in `question` tool", "Do not substitute a question in ordinary chat", "unavailable, denied, or dismissed", "never infer consent"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := hPromptText(t, tc.file)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("%s missing %q", tc.file, want)
				}
			}
			for _, forbid := range tc.forbid {
				if strings.Contains(text, forbid) {
					t.Errorf("%s retains conflicting instruction %q", tc.file, forbid)
				}
			}
		})
	}
}

func TestExecutionPathsLoadSharedDebuggingOnFailure(t *testing.T) {
	for _, file := range []string{
		"skills/onto-build/SKILL.md", "skills/onto-fix/SKILL.md",
		"skills/onto-tweak/SKILL.md", "skills/to-do/SKILL.md",
	} {
		text := hPromptText(t, file)
		if !strings.Contains(text, "../homonto/references/debugging.md") {
			t.Errorf("%s must load shared debugging for failures", file)
		}
	}
}

func TestPresetPoliciesShareEligibilityAndKeepRepairContracts(t *testing.T) {
	for _, preset := range []string{"fix", "tweak"} {
		text := hPromptText(t, "skills/onto-"+preset+"/SKILL.md")
		for _, want := range []string{"workflow-selection.md#eligibility-and-escalation", "execution.md", "inline Owner/Repo/Cwd/Files/Change/Verify repair task", "recorded phase", "Invalidate the old passing result", "no style receipt required"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", preset, want)
			}
		}
		for _, forbid := range []string{"5+ new test cases", "config **keys are added or removed**", "scope exceeds a single function/module"} {
			if strings.Contains(text, forbid) {
				t.Errorf("%s duplicates obsolete eligibility %q", preset, forbid)
			}
		}
	}
}

func TestWorkflowTriggersAndTechnicalProse(t *testing.T) {
	for _, standalone := range []string{"grilling", "handoff"} {
		text := hPromptText(t, "skills/"+standalone+"/SKILL.md")
		if !strings.Contains(text, "OpenCode's built-in `question` tool") || !strings.Contains(text, "standalone mode") {
			t.Errorf("%s must preserve native question behavior without shared skills", standalone)
		}
	}
	for _, workflow := range []string{"onto", "to"} {
		text := hPromptText(t, "skills/"+workflow+"/SKILL.md")
		if !strings.Contains(text, "explicitly selected") || !strings.Contains(text, "informational questions do not start a change") {
			t.Errorf("%s description must distinguish selected execution from questions", workflow)
		}
		for _, file := range []string{"SKILL.md", "references/phrases.md", "references/structures.md", "references/examples.md"} {
			text := hPromptText(t, "skills/"+workflow+"-no-slop/"+file)
			for _, forbid := range []string{"Kill all adverbs", "all adverbs", "Every sentence needs a human subject", "No passive constructions", "Two items beat three", "No em dashes"} {
				if strings.Contains(text, forbid) {
					t.Errorf("%s/%s retains mechanical ban %q", workflow, file, forbid)
				}
			}
		}
		style := hPromptText(t, "skills/"+workflow+"-no-slop/SKILL.md")
		for _, want := range []string{"Preserve accuracy", "normative requirements", "literal commands", "captured output", "No numeric score"} {
			if !strings.Contains(style, want) {
				t.Errorf("%s prose policy lacks %q", workflow, want)
			}
		}
	}
}

func TestExtractedWorkflowPoliciesMaterialize(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, workflow := range []string{"onto", "to"} {
		t.Run(workflow, func(t *testing.T) {
			entries, err := c.Expand([]string{workflow})
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name)
			}
			dir := t.TempDir()
			if err := c.Materialize(dir, names, "none", "none", "", nil); err != nil {
				t.Fatal(err)
			}
			files := []string{"homonto/references/execution.md", "homonto/references/workflow-selection.md", "homonto/references/autonomy.md", "homonto/references/debugging.md"}
			if workflow == "onto" {
				files = append(files, "onto/references/discovery.md", "onto/references/recovery.md", "onto-close/references/integration.md", "onto-build/SKILL.md", "onto-build/references/subagent-protocol.md", "onto-fix/SKILL.md", "onto-tweak/SKILL.md")
			} else {
				files = append(files, "to-do/SKILL.md")
			}
			for _, file := range files {
				data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
				if err != nil || len(data) == 0 {
					t.Fatalf("installed policy %s absent/empty: %v", file, err)
				}
				// New reference extraction must preserve relative links, not merely
				// leave files somewhere in the embedded catalog.
				for _, match := range regexp.MustCompile(`\]\(([^)#]+\.md)(?:#[^)]*)?\)`).FindAllSubmatch(data, -1) {
					target := filepath.Join(dir, filepath.Dir(filepath.FromSlash(file)), string(match[1]))
					if _, err := os.Stat(target); err != nil {
						t.Errorf("installed %s has broken policy link %s: %v", file, match[1], err)
					}
				}
			}
		})
	}
}
