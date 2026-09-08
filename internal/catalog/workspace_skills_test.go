package catalog

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func TestStandaloneSkillsMaterializeWithoutSharedPolicy(t *testing.T) {
	cl, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"handoff", "grilling"} {
		t.Run(name, func(t *testing.T) {
			dst := t.TempDir()
			if err := cl.Materialize(dst, []string{name}, "none", "none", "", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dst, "homonto")); !os.IsNotExist(err) {
				t.Fatalf("fixture must exercise an absent shared skill, got %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dst, name, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"When installed", "Standalone fallback", "shared homonto skill is optional", "host cwd", "conversation", "`.env`"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("standalone installed skill lacks %q", want)
				}
			}
		})
	}
}

// Prompt contracts complement workflow_sequence_test.go; these assertions do
// not prove that an agent executes the instructions or that GitHub accepts them.
func TestWorkspacePromptAuditContracts(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []string
	}{
		{"skills/homonto/references/workspace-policy.md", []string{
			"Legacy schemas 0/1 retain the implicit config source", "Schema 2 never gains this fallback",
			"Managed initialization must happen before creating README files",
			"Allocate immediately after `new`, before any records or source commit",
			"Legacy `to` also supports serial combined isolation without `worktrees.dir`",
			"homonto worktree receiver <change> --workflow onto --repo <alias> --json",
			"never switch a dirty original automatically", "--role receiver",
		}},
		{"skills/onto-fix/SKILL.md", []string{"recorded open/design", "setup resume", "never rerun `new`", "reduced preset template", "proposal-approved", "before any records/source commit"}},
		{"skills/onto-tweak/SKILL.md", []string{"recorded open/design", "setup resume", "never rerun `new`", "reduced preset template", "proposal-approved", "before any records/source commit"}},
		{"skills/onto-open/references/preset-proposal.md", []string{"## Non-Goals", "## Capability Impact", "## Acceptance Scenarios", "## Grounding", "Scenario-ID: <id>", "Owner, Repo, Cwd, Files, Change, and Verify"}},
		{"skills/onto-close/references/lint-checklist.md", []string{"preset-proposal.md", "Every preset Acceptance Scenario has current report evidence"}},
		{"skills/onto-verify/SKILL.md", []string{"do not record receipts yet", "finalize `verification.md`", "Do not edit the report after receipts", "repository/task/scenario supersedes earlier claims", "Re-record all claims", "no separate round-reset command"}},
		{"skills/to/SKILL.md", []string{"check bounded fit before allocation", "Registered bindings currently block promotion", "explicit handoff", "location-aware recovery", "to doctor"}},
		{"skills/to-do/SKILL.md", []string{"`Owner: coordinator` records tasks", "however substantial", "Owner/Repo/Cwd/Files/Change/Verify"}},
		{"skills/onto-build/references/subagent-protocol.md", []string{"explicit separate lane", "Implementers remain prohibited from editing records", "Owner, Repo, absolute Cwd"}},
		{"skills/onto-build/references/plan.md", []string{"- Owner:", "- Repo:", "- Cwd:", "never insert or reorder", "fresh final-validation task"}},
		{"skills/to-done/SKILL.md", []string{"complete evidence pack", "literal Final Verify command, exit status and full output", "Integration verification before push", "actual receiving checkout", "reverified candidate", "--workflow to --repo <alias> --json"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			text := hPromptText(t, tc.file)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q", want)
				}
			}
		})
	}
	for _, name := range []string{"homonto", "h-spike", "h-review", "onto-explorer", "onto-implementer", "onto-reviewer", "onto-skeptic", "to-explorer", "to-implementer", "to-reviewer", "to-skeptic"} {
		text := hPromptText(t, "subagents/"+name+".md")
		for _, want := range []string{"Repo and absolute Cwd", "websearch is optional"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
	}
}

func TestWorkspaceSkillsPolicyReachability(t *testing.T) {
	const policy = "skills/homonto/references/workspace-policy.md"
	links := regexp.MustCompile(`\]\(([^)]+\.md)\)`)
	files, err := fs.Glob(embedded.FS, "skills/*/SKILL.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("skill inventory: %v (%d files)", err, len(files))
	}
	// Include every entry, not only the workflow sub-skills whose dispatcher
	// normally loads the policy for them. Direct invocation must be safe too.
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			seen := map[string]bool{}
			var reachesPolicy func(string) bool
			reachesPolicy = func(file string) bool {
				if seen[file] || !strings.HasPrefix(file, "skills/") {
					return false
				}
				seen[file] = true
				content, err := fs.ReadFile(embedded.FS, file)
				if err != nil {
					return false // Generated/optional references are not policy links.
				}
				if file == policy {
					return true
				}
				for _, link := range links.FindAllSubmatch(content, -1) {
					if reachesPolicy(path.Join(path.Dir(file), string(link[1]))) {
						return true
					}
				}
				return false
			}
			if !reachesPolicy(file) {
				t.Fatal("no working link chain to shared workspace/dirty policy")
			}
		})
	}
}

func TestWorkspaceSkillsAllocationModes(t *testing.T) {
	for _, file := range []string{
		"skills/onto-build/references/subagent-protocol.md",
		"skills/onto-build/references/worktree-protocol.md",
	} {
		t.Run(file, func(t *testing.T) {
			text := hPromptText(t, file)
			_, modes, ok := strings.Cut(text, "## Schema 2 registered allocation")
			if !ok {
				t.Fatal("missing schema 2 allocation policy")
			}
			registered, legacy, ok := strings.Cut(modes, "## Legacy schema 0/1 combined parallel")
			if !ok {
				t.Fatal("missing legacy combined protocol")
			}
			for _, want := range []string{"one binding per workflow/change/repo", "same-repo tasks serially", "No unregistered raw/native task worktrees"} {
				if !strings.Contains(registered, want) {
					t.Errorf("schema 2 missing %q", want)
				}
			}
			if strings.Contains(registered, "git worktree add") || strings.Contains(registered, "One **git worktree per implementer**") {
				t.Error("legacy task allocation leaked into schema 2")
			}
			for _, want := range []string{"coordinator", "disjoint", "Never downgrade", "denial"} {
				if !strings.Contains(legacy, want) {
					t.Errorf("legacy policy missing %q", want)
				}
			}
			if strings.HasSuffix(file, "/worktree-protocol.md") {
				for _, want := range []string{"git worktree add -b", "--dir \"<configRoot>\"", "never a task worktree as its state owner", "without copying `.env` or untracked input automatically"} {
					if !strings.Contains(legacy, want) {
						t.Errorf("legacy worktree mechanics missing %q", want)
					}
				}
			}
			for _, obsolete := range []string{"Default and only safe path", "If a native worktree/sandbox tool is available, use it", "copy any untracked but required local config/`.env`"} {
				if strings.Contains(text, obsolete) {
					t.Errorf("conflicting or unsafe global instruction %q", obsolete)
				}
			}
		})
	}
	for _, file := range []string{"skills/onto-build/SKILL.md", "skills/onto/SKILL.md", "skills/homonto/references/workspace-policy.md", "subagents/homonto.md", "subagents/onto-implementer.md"} {
		text := hPromptText(t, file)
		for _, want := range []string{"Schema 2", "Legacy schema 0/1", "subagent-protocol.md"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s must distinguish allocation modes: missing %q", file, want)
			}
		}
	}
}

func TestWorkspaceSkillsContracts(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []string
	}{
		{"skills/homonto/references/workspace-policy.md", []string{
			"references/workspace.md", "homonto workspace inspect --json",
			"workflow.git", "`existing` (default) or `managed`", "no implicit config repository",
			"--dir \"<configRoot>\"", "homonto workspace init --yes",
			"actionable exact paths", "ask one concrete question before writes",
			"Read-only research proceeds", "same unchanged dirt decision",
			"Preserve is not a gate waiver", "user decides whether and how to transport",
			"Never automatically copy `.env`", "approval of the exact named action and paths",
			"Never automatically stash, reset, delete, or commit user changes",
			"homonto worktree create <change> --workflow onto --repo <alias> --base <ref> --branch <name> --json",
			"homonto worktree remove <change> --workflow onto --repo <alias> --yes",
			"one binding per workflow/change/repo", "same-repo implementation tasks serially",
			"onto set base-ref <change> <commit> --repo <alias>",
			"onto set base-branch <change> <branch> --repo <alias>",
			"--base api=main --base web=develop", "current committed HEAD and local branch",
			"both the exact frozen commit and target branch", "same commit under a different branch",
			"original HEAD, index, and dirty files stay untouched",
			"onto evidence record <change> --repo <alias>",
			"onto complete-integration <change> --repo <alias>",
			"checkpoint their own record changes automatically", "`--path` is workflow-relative",
			"homonto workspace recover", "Never merge workflow history as source integration",
		}},
		{"skills/onto-build/references/subagent-protocol.md", []string{
			"Task-level bindings are not supported", "Run same-repo tasks serially",
			"coordinator alone writes workflow and task records",
			"homonto workspace checkpoint --path changes/<name>",
		}},
		{"skills/onto-close/SKILL.md", []string{
			"no implicit config entry", "never use a records archive/checkpoint SHA as source",
			"Managed receipt writes checkpoint automatically", "existing combined checkout",
		}},
		{"skills/to-done/SKILL.md", []string{
			"Schema 2 has no implicit config repo", "clean gate uses registered bindings",
			"homonto workspace checkpoint --path tasks/<name>/plan.md",
			"Managed `to done` automatically checkpoints",
		}},
		{"skills/h-resolve-issue/SKILL.md", []string{
			"Config/OpenCode may be non-Git", "never require configRoot's origin to match",
			"--dir \"<configRoot>\"", "--repo <alias> --receipt",
		}},
		{"skills/h-continue-pr/SKILL.md", []string{
			"may be non-Git", "not configRoot's origin", "--dir \"<configRoot>\"",
			"Do not retarget existing anchors", "Existing combined mode retains the post-archive pinned commit",
		}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			content, err := fs.ReadFile(embedded.FS, tc.file)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Join(strings.Fields(string(content)), " ")
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing contract %q", want)
				}
			}
		})
	}
}

func TestWorkspaceSkillsCreationBases(t *testing.T) {
	for _, name := range []string{"onto-open", "onto-fix", "onto-tweak", "to", "to-plan", "h-resolve-issue", "h-continue-pr"} {
		text := hPromptText(t, "skills/"+name+"/SKILL.md")
		if !strings.Contains(text, "--base <alias>=<local-branch>") {
			t.Errorf("%s must select per-repo bases at creation", name)
		}
	}
	policy := hPromptText(t, "skills/homonto/references/workspace-policy.md")
	for _, workflow := range []string{"onto", "to"} {
		if !strings.Contains(policy, workflow+" new <change> --repo api --repo web --base api=main --base web=develop") {
			t.Errorf("%s needs a multi-repo creation example with independent target branches", workflow)
		}
	}
	continuation := hPromptText(t, "skills/h-continue-pr/SKILL.md")
	if !strings.Contains(continuation, "--base <alias>=<local-PR-head-branch>") || strings.Contains(continuation, "invent base flags") {
		t.Error("PR continuation must select its local PR-head base at creation, not reject the supported flag")
	}
}

func TestWorkspaceSkillsCoordinatorPermissions(t *testing.T) {
	content, err := fs.ReadFile(embedded.FS, "subagents/homonto.md")
	if err != nil {
		t.Fatal(err)
	}
	fm := parseEmbeddedFrontmatter(t, "subagents/homonto.md", content)
	capabilities := fm["homonto"].(map[string]any)
	allowed := map[string]bool{}
	for _, entry := range capabilities["bash_allow"].([]any) {
		allowed[entry.(string)] = true
	}
	for _, read := range []string{"homonto workspace inspect", "homonto workspace inspect *", "homonto worktree list", "homonto worktree list *"} {
		if !allowed[read] {
			t.Errorf("missing read allowance %q", read)
		}
	}
	for command := range allowed {
		if command == "homonto *" ||
			(strings.HasPrefix(command, "homonto workspace") && command != "homonto workspace inspect" && command != "homonto workspace inspect *") ||
			(strings.HasPrefix(command, "homonto worktree") && command != "homonto worktree list" && command != "homonto worktree list *") {
			t.Errorf("workspace mutation must retain tool permission boundary: %q", command)
		}
	}
}
