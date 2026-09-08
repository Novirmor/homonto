package catalog_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This runs shipped CLI processes against temporary repositories, not prompt
// strings or hand-written workflow state. It proves the sequences, not agent
// compliance with the prompts or live GitHub publication.
func TestWorkflowPromptCLISequences(t *testing.T) {
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Sequence Test", "GIT_AUTHOR_EMAIL": "sequence@example.test",
		"GIT_COMMITTER_NAME": "Sequence Test", "GIT_COMMITTER_EMAIL": "sequence@example.test",
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull,
	} {
		t.Setenv(key, value)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bins := t.TempDir()
	run := func(t *testing.T, cwd, binary string, args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v in %s: %v\n%s", binary, args, cwd, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(t *testing.T, name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, pkg := range map[string]string{"homonto": ".", "onto": "./cmd/onto", "to": "./cmd/to"} {
		run(t, repo, "go", "build", "-o", filepath.Join(bins, name), pkg)
	}
	for _, workflow := range []string{"onto", "to"} {
		for _, early := range []bool{false, true} {
			t.Run(fmt.Sprintf("combined/%s/early=%t", workflow, early), func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "combined")
				write(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version=2\n[frameworks.%s]\nsource='builtin:%s'\nscope='project'\n[workflow]\nroot='docs'\ngit='existing'\n[repos]\napp='.'\n[worktrees]\ndir='../execution'\n", workflow, workflow))
				write(t, filepath.Join(root, ".gitignore"), ".homonto/\n")
				write(t, filepath.Join(root, "source.txt"), "baseline\n")
				// Fixture only: materialized-directory presence is the CLI install gate.
				if err := os.MkdirAll(filepath.Join(root, ".homonto/catalog/skills", workflow), 0755); err != nil {
					t.Fatal(err)
				}
				run(t, root, "git", "init", "-b", "main")
				run(t, root, "git", "add", "homonto.toml", ".gitignore", "source.txt")
				run(t, root, "git", "commit", "-m", "baseline")
				base := run(t, root, "git", "rev-parse", "HEAD")
				args := []string{"new", "sequence", "--repo", "app", "--base", "app=main", "--dir", root}
				if workflow == "onto" {
					args = append(args, "--workflow", "tweak")
				}
				run(t, root, filepath.Join(bins, workflow), args...)
				commitRecords := func() {
					run(t, root, "git", "add", "docs")
					run(t, root, "git", "commit", "-m", "record setup")
				}
				if !early {
					commitRecords()
				}
				cmd := exec.Command(filepath.Join(bins, "homonto"), "worktree", "create", "sequence", "--workflow", workflow, "--repo", "app", "--base", "refs/heads/main", "--branch", "change/sequence", "--json")
				cmd.Dir = root
				out, err := cmd.CombinedOutput()
				if !early {
					if err == nil || !strings.Contains(string(out), "base") {
						t.Fatalf("late allocation must reproduce frozen-base refusal: %v\n%s", err, out)
					}
					return
				}
				if err != nil {
					t.Fatalf("early allocation: %v\n%s", err, out)
				}
				var binding struct{ Path string }
				if err := json.Unmarshal(out, &binding); err != nil || binding.Path == "" {
					t.Fatalf("binding: %v\n%s", err, out)
				}
				if got := run(t, binding.Path, "git", "rev-parse", "HEAD"); got != base {
					t.Fatalf("binding HEAD=%s, want frozen %s", got, base)
				}
				commitRecords()
				run(t, root, filepath.Join(bins, "homonto"), "worktree", "list", "--json")
				if got := run(t, binding.Path, "git", "rev-parse", "HEAD"); got != base {
					t.Fatal("records commit advanced source execution binding")
				}
				receiverArgs := []string{"worktree", "receiver", "sequence", "--workflow", workflow, "--repo", "app", "--json"}
				cmd = exec.Command(filepath.Join(bins, "homonto"), receiverArgs...)
				cmd.Dir = root
				out, err = cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "already checked out") {
					t.Fatalf("receiver must refuse occupied target: %v\n%s", err, out)
				}
				// Explicit fixture-owned release, never an automatic dirty cleanup.
				run(t, root, "git", "checkout", "-b", "fixture/control")
				out = []byte(run(t, root, filepath.Join(bins, "homonto"), receiverArgs...))
				var receiver struct{ Path, Role, Branch string }
				if err := json.Unmarshal(out, &receiver); err != nil || receiver.Role != "receiver" || receiver.Branch != "main" || receiver.Path == binding.Path {
					t.Fatalf("receiver binding: %+v %v\n%s", receiver, err, out)
				}
				run(t, root, filepath.Join(bins, "homonto"), "worktree", "list", "--json")
				if workflow == "to" {
					to := filepath.Join(bins, "to")
					plan := filepath.Join(root, "docs/tasks/sequence/plan.md")
					planText := "# Plan\nVerify the isolated continuation fixture.\n- [ ] Update fixture\n  - Owner: coordinator\n  - Repo: app\n  - Cwd: " + binding.Path + "\n  - Files: source.txt\n  - Change: update fixture\n  - Verify: git show HEAD:source.txt returns verified continuation fixture\nFinal Verify: git show HEAD:source.txt\n"
					write(t, plan, planText)
					run(t, root, to, "phase", "sequence", "--dir", root)
					write(t, filepath.Join(binding.Path, "source.txt"), "verified continuation fixture\n")
					run(t, binding.Path, "git", "add", "source.txt")
					run(t, binding.Path, "git", "commit", "-m", "fixture continuation")
					candidate := run(t, binding.Path, "git", "rev-parse", "HEAD")
					write(t, plan, strings.Replace(planText, "- [ ]", "- [x]", 1)+"\n## Verification\nCandidate: "+candidate+"\nTarget: main\nPR fixture identity (not a real GitHub attestation): https://example.test/fixture/source/pull/1\n")
					if got := run(t, binding.Path, "git", "show", "HEAD:source.txt"); got != "verified continuation fixture" {
						t.Fatalf("source verification: %s", got)
					}
					run(t, root, to, "done", "sequence", "--verified", "--evidence", "git show HEAD:source.txt returned verified continuation fixture", "--dir", root)
					probe := exec.Command("git", "merge-base", "--is-ancestor", candidate, "main")
					probe.Dir = root
					if err := probe.Run(); err == nil {
						t.Fatal("fixture unexpectedly integrated before archive recovery")
					} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
						t.Fatalf("ancestry probe: %v", err)
					}
					archives, err := filepath.Glob(filepath.Join(root, "docs/tasks/archive/*-sequence/plan.md"))
					if err != nil || len(archives) != 1 {
						t.Fatalf("archive discovery before local integration: %v %v", archives, err)
					}
					data, err := os.ReadFile(archives[0])
					if err != nil || !strings.Contains(string(data), "Candidate: "+candidate) || !strings.Contains(string(data), "https://example.test/fixture/source/pull/1") {
						t.Fatalf("archived recovery evidence: %v %s", err, data)
					}
					// Reuse the same identity-bound receiver after terminal archival.
					got := run(t, root, filepath.Join(bins, "homonto"), receiverArgs...)
					var resumed struct{ Path string }
					if err := json.Unmarshal([]byte(got), &resumed); err != nil || resumed.Path != receiver.Path {
						t.Fatalf("to archived receiver changed: %v %s", err, got)
					}
					run(t, resumed.Path, "git", "merge", "--no-ff", candidate, "-m", "recover archived continuation")
					if got := run(t, resumed.Path, "git", "show", "HEAD:source.txt"); got != "verified continuation fixture" {
						t.Fatalf("integration verification: %s", got)
					}
				}
			})
		}
	}
	for _, mode := range []string{"merge", "pr"} {
		t.Run("integration/"+mode, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "homonto.toml"), "schema_version=2\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[workflow]\nroot='records'\ngit='existing'\n[repos]\napp='app'\nuntouched='untouched'\n[worktrees]\ndir='execution'\n")
			if err := os.MkdirAll(filepath.Join(root, ".homonto/catalog/skills/onto"), 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"app", "untouched", "records"} {
				dir := filepath.Join(root, name)
				write(t, filepath.Join(dir, "baseline"), "baseline\n")
				run(t, dir, "git", "init", "-b", "main")
				run(t, dir, "git", "add", "baseline")
				run(t, dir, "git", "commit", "-m", "baseline")
			}
			onto, homonto := filepath.Join(bins, "onto"), filepath.Join(bins, "homonto")
			call := func(args ...string) string {
				t.Helper()
				return run(t, root, onto, append(args, "--dir", root)...)
			}
			reject := func(want string, args ...string) {
				t.Helper()
				cmd := exec.Command(onto, append(args, "--dir", root)...)
				cmd.Dir = root
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), want) {
					t.Fatalf("expected %q for %v: %v\n%s", want, args, err, out)
				}
			}
			call("new", "deliver", "--workflow", "tweak", "--repo", "app", "--repo", "untouched")
			out := run(t, root, homonto, "worktree", "create", "deliver", "--workflow", "onto", "--repo", "app", "--base", "refs/heads/main", "--branch", "work/deliver", "--json")
			var binding struct{ Path string }
			if err := json.Unmarshal([]byte(out), &binding); err != nil {
				t.Fatal(err)
			}
			receiverArgs := []string{"worktree", "receiver", "deliver", "--workflow", "onto", "--repo", "app", "--json"}
			run(t, filepath.Join(root, "app"), "git", "checkout", "-b", "fixture/control")
			var receiver struct{ Path string }
			if mode == "merge" { // Preferred preallocation, subsequently reused.
				out = run(t, root, homonto, receiverArgs...)
				if err := json.Unmarshal([]byte(out), &receiver); err != nil {
					t.Fatal(err)
				}
			}
			change := filepath.Join(root, "records/changes/deliver")
			write(t, filepath.Join(change, "proposal.md"), "# Proposal\nPreset: tweak\nVerify a fixture source commit; untouched needs no delivery.\n")
			write(t, filepath.Join(change, "tasks.md"), "- [ ] 1.1 Write and verify fixture [trace #1]\n")
			call("set", "isolation", "deliver", "worktree")
			call("advance", "deliver", "--to", "build")
			write(t, filepath.Join(binding.Path, "feature"), "verified fixture\n")
			run(t, binding.Path, "git", "add", "feature")
			run(t, binding.Path, "git", "commit", "-m", "write fixture")
			candidate := run(t, binding.Path, "git", "rev-parse", "HEAD")
			if got := run(t, binding.Path, "git", "show", "HEAD:feature"); got != "verified fixture" {
				t.Fatalf("fixture verification: %s", got)
			}
			write(t, filepath.Join(change, "tasks.md"), "- [x] 1.1 Write and verify fixture [trace #1]\n")
			call("advance", "deliver")
			write(t, filepath.Join(change, "verification.md"), "# Verification\nResult: pass\ngit show HEAD:feature returned verified fixture; exit 0.\n")
			call("set", "verify-result", "deliver", "pass")
			call("advance", "deliver")
			call("set", "integration", "deliver", mode)
			call("set", "close-confirmed", "deliver", "reviewed fixture source and no-op target")
			call("merge-deltas", "deliver")
			run(t, filepath.Join(root, "records"), "git", "add", "changes")
			run(t, filepath.Join(root, "records"), "git", "commit", "-m", "record verified fixture")
			call("close", "deliver")
			out = run(t, root, homonto, receiverArgs...)
			var resumed struct{ Path, Role string }
			if err := json.Unmarshal([]byte(out), &resumed); err != nil || resumed.Role != "receiver" || (receiver.Path != "" && receiver.Path != resumed.Path) {
				t.Fatalf("terminal receiver: %v %s", err, out)
			}
			base := run(t, resumed.Path, "git", "rev-parse", "HEAD")
			reject("neither already integrated nor tree-identical", "complete-integration", "deliver", "--repo", "app", "--receipt", "unchanged:"+base)
			untouched := run(t, filepath.Join(root, "untouched"), "git", "rev-parse", "HEAD")
			reject("only valid for a PR receipt", "complete-integration", "deliver", "--repo", "untouched", "--receipt", "unchanged:"+untouched, "--head", untouched)
			call("complete-integration", "deliver", "--repo", "untouched", "--receipt", "unchanged:"+untouched)
			if mode == "pr" {
				// Negative CLI validation only. Do not record a fictional successful
				// external publication claim without an observed GitHub head.
				reject("requires --head", "complete-integration", "deliver", "--repo", "app", "--receipt", "pr:https://example.test/pull/1")
				reject("canonical commit id", "complete-integration", "deliver", "--repo", "app", "--receipt", "pr:https://example.test/pull/1", "--head", "not-an-oid")
				reject("PR head", "complete-integration", "deliver", "--repo", "app", "--receipt", "pr:https://example.test/pull/1", "--head", base)
			} else {
				run(t, resumed.Path, "git", "merge", "--no-ff", candidate, "-m", "integrate verified fixture")
				if got := run(t, resumed.Path, "git", "show", "HEAD:feature"); got != "verified fixture" {
					t.Fatalf("receiver verification: %s", got)
				}
				merge := run(t, resumed.Path, "git", "rev-parse", "HEAD")
				reject("neither already integrated nor tree-identical", "complete-integration", "deliver", "--repo", "app", "--receipt", "unchanged:"+merge)
				reject("only valid for a PR receipt", "complete-integration", "deliver", "--repo", "app", "--receipt", "merge:"+merge, "--head", candidate)
				call("complete-integration", "deliver", "--repo", "app", "--receipt", "merge:"+merge)
			}
			var state struct {
				Derived     string `json:"derived_phase"`
				Integration struct {
					Repositories []struct{ Alias, Receipt, PublicationHead string }
				} `json:"integration_record"`
			}
			out = call("state", "deliver", "--json")
			if err := json.Unmarshal([]byte(out), &state); err != nil || len(state.Integration.Repositories) != 2 {
				t.Fatalf("integration state: %v %s", err, out)
			}
			if (state.Derived == "done") != (mode == "merge") {
				t.Fatalf("unexpected completion: %s", out)
			}
			for _, entry := range state.Integration.Repositories {
				if entry.Alias == "untouched" && entry.Receipt != "unchanged:"+untouched {
					t.Fatalf("no-op receipt lost: %s", out)
				}
				if mode == "pr" && entry.Alias == "app" && (entry.Receipt != "" || entry.PublicationHead != "") {
					t.Fatalf("failed validation fabricated publication: %s", out)
				}
			}
		})
	}
	for _, preset := range []string{"fix", "tweak"} {
		t.Run("preset-resume/"+preset, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "homonto.toml"), "[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n")
			write(t, filepath.Join(root, ".gitignore"), ".homonto/\n")
			if err := os.MkdirAll(filepath.Join(root, ".homonto/catalog/skills/onto"), 0755); err != nil {
				t.Fatal(err)
			}
			run(t, root, "git", "init", "-b", "main")
			run(t, root, "git", "add", "homonto.toml", ".gitignore")
			run(t, root, "git", "commit", "-m", "baseline")
			onto := filepath.Join(bins, "onto")
			run(t, root, onto, "new", "resume", "--workflow", preset, "--dir", root)
			// A new process resumes the scaffold: derived build is not setup completion.
			var state struct {
				Phase   string `json:"phase"`
				Derived string `json:"derived_phase"`
			}
			out := run(t, root, onto, "state", "resume", "--json", "--dir", root)
			if err := json.Unmarshal([]byte(out), &state); err != nil || state.Phase != "open" || state.Derived != "build" {
				t.Fatalf("scaffold routing: %+v %v\n%s", state, err, out)
			}
			run(t, root, "git", "checkout", "-b", preset+"/resume")
			change := filepath.Join(root, "docs/changes/resume")
			write(t, filepath.Join(change, "proposal.md"), "# Proposal: resume\nPreset: "+preset+"\n\n## Why\nCheck resume.\n\n## What Changes\nOne file.\n\n## Non-Goals\nNo new capability.\n\n## Capability Impact\nNo spec-level change.\n\n## Acceptance Scenarios\n- resumed: setup reaches build.\n\n## Grounding\nDirect fixture reads.\n")
			write(t, filepath.Join(change, "tasks.md"), "# Tasks: resume\n\n## 1. Implementation\n- [ ] 1.1 Complete bounded work [trace #1]\n  - Owner: coordinator\n  - Repo: config\n  - Cwd: "+root+"\n  - Files: source.txt\n  - Change: bounded fixture\n  - Verify: git diff --check; exit 0\n")
			for _, pair := range [][2]string{{"isolation", "branch"}, {"build-mode", "direct"}, {"tdd-mode", "direct"}, {"proposal-approved", "2026-09-08 reviewed bounded scope"}} {
				if preset == "fix" && pair[0] == "tdd-mode" {
					pair[1] = "tdd"
				}
				run(t, root, onto, "set", pair[0], "resume", pair[1], "--dir", root)
			}
			run(t, root, "git", "add", "docs")
			run(t, root, "git", "commit", "-m", "complete preset setup")
			run(t, root, onto, "advance", "resume", "--dir", root)
			out = run(t, root, onto, "state", "resume", "--json", "--dir", root)
			if err := json.Unmarshal([]byte(out), &state); err != nil || state.Phase != "design" || state.Derived != "build" {
				t.Fatalf("interrupted preset setup: %v\n%s", err, out)
			}
			run(t, root, onto, "advance", "resume", "--to", "build", "--dir", root)
			out = run(t, root, onto, "state", "resume", "--json", "--dir", root)
			if err := json.Unmarshal([]byte(out), &state); err != nil || state.Phase != "build" {
				t.Fatalf("resumed setup did not reach build: %v\n%s", err, out)
			}
			// A real failing probe followed by a passing rerun exercises no-spec
			// scenario declarations and supersession without fabricating exit codes.
			probeArgs := []string{"rev-parse", "--verify", "refs/heads/probe"}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte("git "+strings.Join(probeArgs, " "))))
			for _, passing := range []bool{false, true} {
				if passing {
					run(t, root, "git", "branch", "probe")
				}
				probe := exec.Command("git", probeArgs...)
				probe.Dir = root
				output, err := probe.CombinedOutput()
				exit := 0
				if err != nil {
					if e, ok := err.(*exec.ExitError); ok {
						exit = e.ExitCode()
					} else {
						t.Fatal(err)
					}
				}
				if (exit == 0) != passing {
					t.Fatalf("probe passing=%t exit=%d: %s", passing, exit, output)
				}
				result := "fail"
				if passing {
					result = "pass"
				}
				report := filepath.Join(change, "verification.md")
				write(t, report, "# Verification\nResult: "+result+"\nScenario-ID: SC-resume\n\nCommand: git "+strings.Join(probeArgs, " ")+fmt.Sprintf("\nExit: %d\n", exit)+string(output))
				outputFile := filepath.Join(t.TempDir(), "probe.txt")
				write(t, outputFile, string(output))
				run(t, root, onto, "evidence", "record", "resume", "--task", "1", "--scenario", "SC-resume", "--exec", "git", "--cmd-hash", hash, "--exit", fmt.Sprint(exit), "--output", outputFile, "--artifact", report, "--dir", root)
			}
			trace := run(t, root, onto, "trace", "resume", "--json", "--dir", root)
			if !strings.Contains(trace, "superseded-by") || !strings.Contains(trace, "SC-resume") {
				t.Fatalf("fresh evidence did not supersede prior claim: %s", trace)
			}
			for _, phase := range []string{"build", "verify"} {
				if phase == "verify" {
					write(t, filepath.Join(change, "tasks.md"), "- [x] 1.1 Verify local probe ref [trace #1]\n")
					run(t, root, onto, "advance", "resume", "--dir", root)
				}
				// Reproduce the old unconditional setup hop; it must not mutate state.
				cmd := exec.Command(onto, "advance", "resume", "--to", "build", "--dir", root)
				cmd.Dir = root
				if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "not before build") {
					t.Fatalf("later setup hop not refused: %v %s", err, out)
				}
				run(t, root, onto, "set", "proposal-approved", "resume", "refreshed setup review without a phase hop", "--dir", root)
				out := run(t, root, onto, "state", "resume", "--json", "--dir", root)
				if err := json.Unmarshal([]byte(out), &state); err != nil || state.Phase != phase {
					t.Fatalf("setup repair changed recorded %s: %v %s", phase, err, out)
				}
			}
		})
	}
	for _, missing := range []string{"", "Owner", "Repo", "Cwd"} {
		t.Run("converted-contract/missing="+missing, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "homonto.toml"), "[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[frameworks.to]\nsource='builtin:to'\nscope='project'\n")
			write(t, filepath.Join(root, ".gitignore"), ".homonto/\n")
			for _, workflow := range []string{"onto", "to"} {
				if err := os.MkdirAll(filepath.Join(root, ".homonto/catalog/skills", workflow), 0755); err != nil {
					t.Fatal(err)
				}
			}
			run(t, root, "git", "init", "-b", "main")
			run(t, root, "git", "add", "homonto.toml", ".gitignore")
			run(t, root, "git", "commit", "-m", "baseline")
			run(t, root, filepath.Join(bins, "onto"), "new", "carry", "--workflow", "tweak", "--dir", root)
			change := filepath.Join(root, "docs/changes/carry")
			write(t, filepath.Join(change, "proposal.md"), "# Proposal\nPreset: tweak\nCarry a concrete task into to.\n")
			write(t, filepath.Join(change, "tasks.md"), "- [ ] 1.1 Inspect baseline [trace #1]\n")
			plan := "# Plan\n\n## Task 1.1 Inspect baseline\n"
			for _, field := range [][2]string{{"Owner", "coordinator"}, {"Repo", "config"}, {"Cwd", root}, {"Files", "homonto.toml"}, {"Do", "preserve the configuration"}, {"Verify", "git diff --check"}} {
				if field[0] != missing {
					plan += "- " + field[0] + ": " + field[1] + "\n"
				}
			}
			write(t, filepath.Join(change, "plan.md"), plan+"\nFinal Verify: git diff --check\n")
			run(t, root, filepath.Join(bins, "onto"), "set", "isolation", "carry", "branch", "--dir", root)
			run(t, root, filepath.Join(bins, "onto"), "advance", "carry", "--to", "build", "--dir", root)
			run(t, root, "git", "add", "docs")
			run(t, root, "git", "commit", "-m", "record conversion input")
			run(t, root, filepath.Join(bins, "onto"), "demote", "carry", "--yes", "--dir", root)
			out := run(t, root, filepath.Join(bins, "to"), "status", "--json", "--dir", root)
			var entries []struct{ Change, Phase, Error string }
			want := "plan"
			if missing == "" {
				want = "do"
			}
			if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 1 || entries[0].Change != "carry" || entries[0].Phase != want || entries[0].Error != "" {
				t.Fatalf("converted contract routing: %v %s", err, out)
			}
			if missing == "" {
				data, err := os.ReadFile(filepath.Join(root, "docs/tasks/carry/plan.md"))
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"Owner: coordinator", "Repo: config", "Cwd: " + root} {
					if !strings.Contains(string(data), field) {
						t.Errorf("conversion lost %q: %s", field, data)
					}
				}
			}
		})
	}
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("managed-init/populated=%t", populated), func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "homonto.toml"), "schema_version=2\n[workflow]\nroot='records'\ngit='managed'\n")
			run(t, root, filepath.Join(bins, "homonto"), "workspace", "inspect", "--json")
			if populated {
				write(t, filepath.Join(root, "records/changes/README.md"), "premature bootstrap\n")
			}
			cmd := exec.Command(filepath.Join(bins, "homonto"), "workspace", "init", "--yes")
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if populated && err == nil {
				t.Fatalf("must reject README-poisoned unowned records: %s", out)
			}
			if !populated && err != nil {
				t.Fatalf("initialize empty managed root: %v\n%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
				t.Fatalf("config root poisoned with Git: %v", err)
			}
			if !populated {
				if _, err := os.Stat(filepath.Join(root, "records/.git")); err != nil {
					t.Fatalf("managed records not initialized: %v", err)
				}
			}
		})
	}
}
