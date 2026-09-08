package workflowstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadReportsBothActiveWorkflowsAndMalformedState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	onto := filepath.Join(root, "docs", "changes", "feature")
	if err := os.MkdirAll(onto, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onto, "onto-state.yaml"), []byte("change: feature\nworkflow: full\nphase: build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onto, "tasks.md"), []byte("- [x] first\n- [ ] second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(root, "docs", "tasks", "chore")
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to, "to-state.yaml"), []byte("change: chore\nphase: do\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to, "plan.md"), []byte("- [ ] remaining\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes", "broken"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Read(root)
	if len(got.Changes) != 2 {
		t.Fatalf("changes = %#v, want two", got.Changes)
	}
	if got.Changes[0].Workflow != "onto" || got.Changes[0].TasksCompleted != 1 || got.Changes[0].TasksTotal != 2 {
		t.Fatalf("onto change = %#v", got.Changes[0])
	}
	if got.Changes[1].Workflow != "to" || got.Changes[1].Pending[0] != "complete planned tasks" {
		t.Fatalf("to change = %#v", got.Changes[1])
	}
	if len(got.Findings) != 1 || got.Findings[0].Change != "broken" {
		t.Fatalf("findings = %#v", got.Findings)
	}
}

func TestReadExactConfigAndTerminalGenerations(t *testing.T) {
	root := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		if strings.HasSuffix(p, "onto-state.yaml") {
			text = "schema_version: 2\n" + text
		}
		p = filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("selected.toml", "[workflow]\nroot = 'records'\n")
	write("homonto.toml", "invalid = [")
	write("records/changes/one/onto-state.yaml", "id: generation2\nchange: one\nworkflow: full\nphase: close\nverify:\n  result: fail\n")
	write("records/changes/archive/2026-09-01-one/onto-state.yaml", "id: generation1\nchange: one\nworkflow: full\nphase: close\narchived: true\nverify:\n  result: fail\n")
	write("records/changes/archive/2026-09-02-pending/onto-state.yaml", "id: pending\nchange: pending\nworkflow: full\nphase: close\narchived: true\nintegration_required: true\n")
	write("records/changes/cancelled/onto-state.yaml", "change: cancelled\nworkflow: full\nphase: open\nabandoned: true\nverify:\n  result: fail\n")
	write("records/tasks/archive/2026-09-01-done/to-state.yaml", "change: done\nphase: done\nverified: true\n")
	write("records/tasks/archive/2026-09-01-cancelled/to-state.yaml", "change: cancelled\nphase: abandoned\n")
	write("records/tasks/archive/2026-09-01-bypass/to-state.yaml", "change: bypass\nphase: done\n")
	write("records/tasks/interrupted/to-state.yaml", "change: interrupted\nphase: done\nverified: true\n")
	write("records/tasks/cancelled/to-state.yaml", "change: cancelled\nphase: abandoned\n")
	got := ReadConfig(filepath.Join(root, "selected.toml"))
	if len(got.Changes) != 9 {
		t.Fatalf("snapshot: %+v", got)
	}
	statuses := map[string]string{}
	for _, c := range got.Changes {
		statuses[c.Path] = c.Status
		if (c.Status == "completed" || c.Status == "abandoned" || c.Status == "bypassed") && len(c.Pending) > 0 {
			t.Fatalf("terminal state retains recovery advice: %+v", c)
		}
		if c.Workflow == "onto" && (c.Status == "completed" || c.Status == "abandoned") && c.VerifyResult != "fail" {
			t.Fatalf("historical verification lost: %+v", c)
		}
		if c.Identity == "" {
			t.Fatalf("missing identity: %+v", c)
		}
		if c.Identity == "generation2" && !strings.Contains(strings.Join(c.Pending, ","), "verification failure") {
			t.Fatalf("failure missing: %+v", c)
		}
	}
	for p, want := range map[string]string{
		"changes/one": "active", "changes/archive/2026-09-01-one": "completed", "changes/archive/2026-09-02-pending": "integration-pending",
		"changes/cancelled": "abandoned", "tasks/archive/2026-09-01-done": "completed", "tasks/archive/2026-09-01-cancelled": "abandoned",
		"tasks/archive/2026-09-01-bypass": "bypassed", "tasks/interrupted": "interrupted", "tasks/cancelled": "abandoned",
	} {
		if statuses[p] != want {
			t.Errorf("%s = %s, want %s", p, statuses[p], want)
		}
	}
	if len(got.Findings) != 1 || got.Findings[0].Change != "pending" {
		t.Fatalf("findings = %+v", got.Findings)
	}
	if got.ConfigPath != filepath.Join(root, "selected.toml") || got.WorkflowRoot != filepath.Join(root, "records") {
		t.Fatalf("lost binding: %+v", got)
	}
}

func TestReadInvalidConfigIsNotHealthyEmpty(t *testing.T) {
	for _, text := range []string{"invalid = [", "[workflow]\nroot = '../escape'\n", "schema_version = 99\n"} {
		t.Run(text, func(t *testing.T) {
			root := t.TempDir()
			cfg := filepath.Join(root, "custom.toml")
			if err := os.WriteFile(cfg, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := ReadConfig(cfg); len(got.Findings) == 0 || len(got.Changes) != 0 {
				t.Fatalf("invalid config appears healthy: %+v", got)
			}
		})
	}
	if got := ReadConfig(filepath.Join(t.TempDir(), "missing.toml")); len(got.Findings) == 0 {
		t.Fatal("missing config appears healthy")
	}
}

func TestLegacyIdentitySurvivesArchiveMove(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs/tasks/one")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := []byte("change: one\nphase: do\ncreated: 2026-09-08\n")
	if err := os.WriteFile(filepath.Join(dir, "to-state.yaml"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	before := Read(root)
	archive := filepath.Join(root, "docs/tasks/archive")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, filepath.Join(archive, "2026-09-08-one")); err != nil {
		t.Fatal(err)
	}
	if after := Read(root); len(after.Changes) != 1 || after.Changes[0].Identity != before.Changes[0].Identity {
		t.Fatalf("move lost identity: %+v -> %+v", before, after)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "to-state.yaml"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	after := Read(root)
	if len(after.Changes) != 2 || after.Changes[0].Identity == after.Changes[1].Identity {
		t.Fatalf("same-day generation collided: %+v", after)
	}
}
