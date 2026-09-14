package workflowstatus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func handoffWrite(t *testing.T, root, path, text string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func handoffFixture(t *testing.T, workflow string) (string, string) {
	t.Helper()
	root := t.TempDir()
	handoffWrite(t, root, "selected.toml", "[workflow]\nroot = 'records'\n")
	handoffWrite(t, root, "homonto.toml", "invalid = [")
	dir, state := "records/tasks/demo", "id: generation\nchange: demo\nphase: do\n"
	if workflow == "onto" {
		dir = "records/changes/demo"
		state = "schema_version: 2\nid: generation\nchange: demo\nworkflow: fix\nphase: build\nisolation: branch\nintegration: pr\nbuild_mode: direct\ntdd_mode: tdd\nverify:\n  scale: light\n  result: pending\n"
	}
	handoffWrite(t, root, dir+"/"+workflow+"-state.yaml", state)
	return root, dir
}

func handoffLayoutMarker(t *testing.T, root string) {
	t.Helper()
	b, err := json.Marshal(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: filepath.Join(root, "selected.toml"), WorkflowRoot: filepath.Join(root, "records"), GitMode: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	handoffWrite(t, root, ".homonto/"+workflowroot.LayoutMarkerFile, string(b))
}

func handoffBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadHandoffExactConfigRecoveryAndNoWrites(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		t.Run(workflow, func(t *testing.T) {
			root, dir := handoffFixture(t, workflow)
			tasks := "plan.md"
			if workflow == "onto" {
				tasks = "tasks.md"
			}
			block := "- [ ] next task\n  Files: internal/example.go\n  Verify: go test ./internal/example\n  Acceptance: complete this entire block\n"
			handoffWrite(t, root, dir+"/"+tasks, "- [x] previous\n"+block+"- [ ] later task\n")
			handoffWrite(t, root, dir+"/proposal.md", "Objective: recover the exact generation\n")
			handoffWrite(t, root, dir+"/notes.md", strings.Repeat("old note\n", 10000)+"LATEST RECOVERY NOTE\n")
			handoffWrite(t, root, dir+"/verification.md", "Result: pending\nRECENT VERIFICATION\n")
			cfg := filepath.Join(root, "selected.toml")
			before := handoffBytes(t, root)
			snapshot := ReadConfig(cfg)
			got, err := ReadHandoff(cfg, workflow, "demo", "generation")
			if err != nil {
				t.Fatal(err)
			}
			if got.SchemaVersion != 1 || got.ConfigPath != cfg || got.ConfigRoot != root || got.WorkflowRoot != filepath.Join(root, "records") || !reflect.DeepEqual(got.Change, snapshot.Changes[0]) {
				t.Fatalf("handoff = %+v", got)
			}
			if got.Sources[""] != root || got.NextSkill != workflow+map[string]string{"onto": "-build", "to": "-do"}[workflow] {
				t.Fatalf("routing = %+v", got)
			}
			text, total := "", 0
			for _, a := range got.Artifacts {
				if !filepath.IsAbs(a.Path) || !strings.HasPrefix(a.Path, filepath.Join(root, dir)+string(filepath.Separator)) {
					t.Fatalf("artifact path = %q", a.Path)
				}
				text += a.Text
				total += len(a.Text)
			}
			if total > 12*1024 || !got.Truncated || !strings.Contains(text, block) || !strings.Contains(text, "LATEST RECOVERY NOTE") || !strings.Contains(text, "RECENT VERIFICATION") {
				t.Fatalf("recovery text (%d): %s", total, text)
			}
			if workflow == "onto" && (got.Decisions["integration"] != "pr" || got.Decisions["tdd_mode"] != "tdd" || got.Decisions["verify.result"] != "pending") {
				t.Fatalf("decisions = %+v", got.Decisions)
			}
			if !reflect.DeepEqual(before, handoffBytes(t, root)) {
				t.Fatal("handoff wrote files")
			}
			if !reflect.DeepEqual(snapshot, ReadConfig(cfg)) {
				t.Fatal("handoff changed snapshot")
			}
		})
	}
}

func TestReadHandoffSelectsArchiveGeneration(t *testing.T) {
	root, _ := handoffFixture(t, "to")
	handoffWrite(t, root, "records/tasks/archive/old-demo/to-state.yaml", "id: old\nchange: demo\nphase: done\nverified: true\n")
	handoffWrite(t, root, "records/tasks/archive/old-demo/plan.md", "ARCHIVED PLAN\n")
	got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Change.Path != "tasks/archive/old-demo" || len(got.Sources) != 0 || got.NextSkill != "" {
		t.Fatalf("wrong generation: %+v", got)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), "ARCHIVED PLAN") || !strings.Contains(string(b), "archived generation") {
		t.Fatalf("archive recovery = %s", b)
	}
	handoffWrite(t, root, "records/tasks/archive/duplicate/to-state.yaml", "id: old\nchange: demo\nphase: done\n")
	if _, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "old"); err == nil {
		t.Fatal("accepted ambiguous generation")
	}
}

func TestReadHandoffRejectsSelectorsAndMalformedRecords(t *testing.T) {
	root, dir := handoffFixture(t, "to")
	cfg := filepath.Join(root, "selected.toml")
	for _, args := range [][3]string{{"../to", "demo", "generation"}, {"to", "../demo", "generation"}, {"to", "demo/x", "generation"}, {"to", "archive", "generation"}, {"to", "demo", ""}, {"to", "demo", "../generation"}, {"to", "demo", "generation\n"}, {"to", "demo", "missing"}} {
		if _, err := ReadHandoff(cfg, args[0], args[1], args[2]); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	handoffWrite(t, root, dir+"/to-state.yaml", "id: generation\nchange: demo\nphase: invalid\n")
	if _, err := ReadHandoff(cfg, "to", "demo", "generation"); err == nil {
		t.Fatal("accepted malformed record")
	}
}

func TestReadHandoffUnsafeArtifactsAndOversizedTask(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "huge-line", "huge-task"} {
		t.Run(kind, func(t *testing.T) {
			root, dir := handoffFixture(t, "onto")
			path := filepath.Join(root, dir, "tasks.md")
			switch kind {
			case "symlink":
				handoffWrite(t, root, "secret", "DO NOT READ SECRET\n")
				if err := os.Symlink(filepath.Join(root, "secret"), path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			default:
				handoffWrite(t, root, dir+"/tasks.md", "- [ ] unfinished\n"+strings.Repeat("x", 100000)+"\n- [ ] another\n")
			}
			got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "onto", "demo", "generation")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), "DO NOT READ SECRET") || strings.Contains(string(b), "- [ ] unfinished") || len(got.Findings) == 0 {
				t.Fatalf("unsafe/partial recovery: %s", b)
			}
			if kind == "huge-task" || kind == "huge-line" {
				if !got.Truncated {
					t.Fatal("missing truncation")
				}
			}
		})
	}
}

func TestReadHandoffRejectsSymlinkedRecordParent(t *testing.T) {
	root, dir := handoffFixture(t, "to")
	original := filepath.Join(root, dir)
	if err := os.Rename(original, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), original); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "generation"); err == nil {
		t.Fatal("followed record symlink")
	}
}

func TestReadHandoffLegacyIdentityAfterArchiveAndNameReuse(t *testing.T) {
	root, dir := handoffFixture(t, "to")
	handoffWrite(t, root, dir+"/to-state.yaml", "change: demo\nphase: do\ncreated: 2026-09-12\n")
	cfg := filepath.Join(root, "selected.toml")
	identity := ReadConfig(cfg).Changes[0].Identity
	archive := filepath.Join(root, "records/tasks/archive")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, dir), filepath.Join(archive, "old-demo")); err != nil {
		t.Fatal(err)
	}
	handoffWrite(t, root, "records/tasks/archive/old-demo/to-state.yaml", "change: demo\nphase: done\nverified: true\ncreated: 2026-09-12\n")
	handoffWrite(t, root, dir+"/to-state.yaml", "change: demo\nphase: do\ncreated: 2026-09-12\n")
	got, err := ReadHandoff(cfg, "to", "demo", identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.Change.Path != "tasks/archive/old-demo" || got.Change.Identity != identity || len(got.Sources) != 0 {
		t.Fatalf("wrong legacy generation: %+v", got)
	}
}

func TestReadHandoffExplicitBindingAndScopeValidation(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		t.Run(workflow, func(t *testing.T) {
			root, dir := handoffFixture(t, workflow)
			repo := filepath.Join(root, "source")
			if err := os.Mkdir(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-b", "main")
			git("-c", "user.name=Handoff Test", "-c", "user.email=handoff@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base")
			base, common := git("rev-parse", "HEAD"), git("rev-parse", "--path-format=absolute", "--git-common-dir")
			handoffWrite(t, root, "selected.toml", "schema_version = 2\n[workflow]\nroot = 'records'\n[worktrees]\ndir = 'execution'\n[repos]\napp = 'source'\n")
			handoffLayoutMarker(t, root)
			schema, phase, extra := 1, "do", ""
			if workflow == "onto" {
				schema, phase, extra = 3, "build", "workflow: fix\n"
			}
			state := fmt.Sprintf("schema_version: %d\nid: generation\nchange: demo\nphase: %s\n%srepo_mode: explicit\nrepos: [app]\nrepo_bases:\n  app:\n    base_ref: %s\n    base_branch: main\n    git_common_dir: %q\n", schema, phase, extra, base, common)
			handoffWrite(t, root, dir+"/"+workflow+"-state.yaml", state)
			cfg := filepath.Join(root, "selected.toml")
			l, err := workspace.Load(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReadHandoff(cfg, workflow, "demo", "generation")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Sources) != 1 || got.Sources["app"] != repo {
				t.Fatalf("explicit sources: %+v", got)
			}
			binding, err := workspace.CreateWorktree(l, workflow, "demo", "app", "main", "work/demo")
			if err != nil {
				t.Fatal(err)
			}
			before := handoffBytes(t, root)
			got, err = ReadHandoff(cfg, workflow, "demo", "generation")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Sources) != 1 || got.Sources["app"] != binding.Path {
				t.Fatalf("bound sources: %+v", got)
			}
			if !reflect.DeepEqual(before, handoffBytes(t, root)) {
				t.Fatal("binding observation wrote files")
			}
			owner := filepath.Join(binding.GitDir, "homonto-owner")
			if err := os.WriteFile(owner, []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err = ReadHandoff(cfg, workflow, "demo", "generation")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(got)
			if len(got.Sources) != 0 || !strings.Contains(string(b), "creation token mismatch") {
				t.Fatalf("corrupt binding exposed: %s", b)
			}
			if err := os.WriteFile(owner, []byte(binding.Ownership), 0o600); err != nil {
				t.Fatal(err)
			}
			handoffWrite(t, root, dir+"/"+workflow+"-state.yaml", strings.Replace(state, common, filepath.Join(root, "wrong.git"), 1))
			got, err = ReadHandoff(cfg, workflow, "demo", "generation")
			if err != nil {
				t.Fatal(err)
			}
			b, _ = json.Marshal(got)
			if len(got.Sources) != 0 || !strings.Contains(string(b), "common-dir mismatch") {
				t.Fatalf("changed authority exposed: %s", b)
			}
		})
	}
}

func TestReadHandoffMalformedRegistryAndLegacyConfigUpgrade(t *testing.T) {
	root, _ := handoffFixture(t, "to")
	cfg := filepath.Join(root, "selected.toml")
	handoffWrite(t, root, "selected.toml", "schema_version = 2\n[workflow]\nroot = 'records'\n")
	handoffLayoutMarker(t, root)
	got, err := ReadHandoff(cfg, "to", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 1 || got.Sources[""] != root {
		t.Fatalf("legacy source lost after upgrade: %+v", got)
	}
	handoffWrite(t, root, ".homonto/worktrees.json", "{malformed}")
	got, err = ReadHandoff(cfg, "to", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if len(got.Sources) != 0 || !strings.Contains(string(b), "invalid registry") {
		t.Fatalf("invalid registry exposed sources: %s", b)
	}
}

func TestReadHandoffPrioritizesFullTaskBlock(t *testing.T) {
	root, dir := handoffFixture(t, "to")
	block := "- [ ] current contract\n  - Files: example.go\n  - Change: preserve nested details\n    - [ ] nested acceptance\n  - Verify: focused command\n"
	handoffWrite(t, root, dir+"/plan.md", "Objective: important objective\n"+strings.Repeat("- [x] earlier task\n", 900)+block+"- [ ] later\n\n## Notes\nMost recent embedded note\n\n## Verification\nLatest embedded verification\n")
	got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Artifacts[0].Text, block) || strings.Contains(got.Artifacts[0].Text, "earlier task") {
		t.Fatalf("task recovery = %s", got.Artifacts[0].Text)
	}
	for _, want := range []string{"important objective", "Most recent embedded note", "Latest embedded verification"} {
		if !strings.Contains(got.Artifacts[0].Text, want) {
			t.Fatalf("missing %q: %s", want, got.Artifacts[0].Text)
		}
	}
	block = "- [ ] large but fitting task\n" + strings.Repeat("  acceptance detail\n", 550)
	handoffWrite(t, root, dir+"/plan.md", strings.Repeat("- [x] earlier task\n", 900)+block+"- [ ] later\n")
	got, err = ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Artifacts[0].Text, block) {
		t.Fatal("fitting full task was omitted")
	}
}

func TestReadHandoffRecoveryBudgetPrioritizesRecentEvidenceOverCheckedHistory(t *testing.T) {
	root, dir := handoffFixture(t, "to")
	handoffWrite(t, root, dir+"/plan.md", strings.Repeat("- [x] completed task history\n", 425)+"- [ ] remaining task\n  Verify: exact command\n")
	handoffWrite(t, root, dir+"/notes.md", strings.Repeat("old note\n", 10000)+"MOST RECENT NOTE\n")
	handoffWrite(t, root, dir+"/verification.md", strings.Repeat("old evidence\n", 10000)+"MOST RECENT VERIFICATION\n")
	got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, artifact := range got.Artifacts {
		text += artifact.Text
	}
	if len(text) > 12*1024 || !strings.Contains(text, "MOST RECENT NOTE") || !strings.Contains(text, "MOST RECENT VERIFICATION") || !strings.Contains(text, "remaining task\n  Verify: exact command") {
		t.Fatalf("priority recovery: %s", text)
	}
}

func TestReadHandoffOversizedBoundedTaskOmitted(t *testing.T) {
	root, dir := handoffFixture(t, "to")
	handoffWrite(t, root, dir+"/plan.md", "- [ ] unfinished\n"+strings.Repeat("  contract\n", 1600)+"- [ ] next\n")
	got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "to", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || !got.Artifacts[0].Truncated || !strings.Contains(got.Artifacts[0].Text, "block omitted") || strings.Contains(got.Artifacts[0].Text, "- [ ] unfinished") {
		t.Fatalf("partial block: %+v", got.Artifacts[0])
	}
}

func TestReadHandoffUsesNativePhaseEvidence(t *testing.T) {
	root, dir := handoffFixture(t, "onto")
	handoffWrite(t, root, dir+"/tasks.md", "- [x] complete\n")
	got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "onto", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if got.Change.DerivedPhase != "verify" || got.NextSkill != "onto-verify" {
		t.Fatalf("phase = %+v", got)
	}
	handoffWrite(t, root, dir+"/design.md", "Status: Under revision\n")
	got, err = ReadHandoff(filepath.Join(root, "selected.toml"), "onto", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if got.Change.DerivedPhase != "design" || got.NextSkill != "onto-design" {
		t.Fatalf("phase = %+v", got)
	}
}

func TestReadHandoffOntoArchiveAndUnsafeEvidenceParents(t *testing.T) {
	root, _ := handoffFixture(t, "onto")
	dir := "records/changes/archive/old-demo"
	handoffWrite(t, root, dir+"/onto-state.yaml", "schema_version: 2\nid: old\nchange: demo\nworkflow: full\nphase: close\narchived: true\nintegration_required: true\n")
	handoffWrite(t, root, dir+"/proposal.md", "Archived objective\n")
	cfg := filepath.Join(root, "selected.toml")
	got, err := ReadHandoff(cfg, "onto", "demo", "old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Change.Status != "integration-pending" || got.NextSkill != "onto-close" || len(got.Sources) != 0 {
		t.Fatalf("archive = %+v", got)
	}
	if err := os.Symlink(root, filepath.Join(root, dir, ".onto")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHandoff(cfg, "onto", "demo", "old"); err == nil {
		t.Fatal("followed sidecar parent")
	}
}

func TestReadHandoffRejectsUnsafeStateAndConfig(t *testing.T) {
	for _, target := range []string{"state", "config", "config-parent", "records-parent", "oversized-state"} {
		t.Run(target, func(t *testing.T) {
			root, dir := handoffFixture(t, "to")
			cfg := filepath.Join(root, "selected.toml")
			path := filepath.Join(root, dir, "to-state.yaml")
			switch target {
			case "config":
				path = cfg
			case "config-parent":
				path = root
			case "records-parent":
				path = filepath.Join(root, "records", "tasks")
			case "oversized-state":
				handoffWrite(t, root, dir+"/to-state.yaml", "id: generation\nchange: demo\nphase: do\nevidence: "+strings.Repeat("x", 70000))
			}
			if target != "oversized-state" {
				destination := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(path, destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadHandoff(cfg, "to", "demo", "generation"); err == nil {
				t.Fatal("read unsafe input")
			}
		})
	}
}

func TestReadHandoffJSONContractAndUTF8Budget(t *testing.T) {
	root, dir := handoffFixture(t, "onto")
	handoffWrite(t, root, dir+"/tasks.md", "- [ ] work\n  complete task contract\n")
	for _, name := range []string{"proposal.md", "notes.md", "verification.md", "plan.md"} {
		handoffWrite(t, root, dir+"/"+name, strings.Repeat("界", 25000)+"recent\n")
	}
	got, err := ReadHandoff(filepath.Join(root, "selected.toml"), "onto", "demo", "generation")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{"schemaVersion", "configPath", "configRoot", "workflowRoot", "change", "sources", "artifacts", "decisions", "findings", "nextSkill", "truncated", "note"}
	if len(fields) != len(want) {
		t.Fatalf("fields = %v", fields)
	}
	for _, key := range want {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			t.Fatalf("missing/null %s", key)
		}
	}
	var decoded Handoff
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, artifact := range decoded.Artifacts {
		total += len(artifact.Text)
	}
	if total > 12*1024 || !got.Truncated {
		t.Fatalf("UTF8 budget = %d", total)
	}
}
