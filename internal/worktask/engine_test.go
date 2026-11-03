package worktask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/catalog"
)

// TestBuiltinEnginesAreDescriptorCases documents today's builtin task
// behavior as properties of each engine's declared contract (ADR 0093).
func TestBuiltinEnginesAreDescriptorCases(t *testing.T) {
	s := fixture(t)
	for _, tc := range []struct {
		name      string
		lifecycle string
	}{
		{"no", catalog.LifecycleStateless},
		{"onto", catalog.LifecycleNative},
		{"to", catalog.LifecycleNative},
	} {
		e, err := s.engine(tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !e.Builtin || e.Lifecycle != tc.lifecycle {
			t.Errorf("%s: builtin=%v lifecycle=%q, want builtin %q", tc.name, e.Builtin, e.Lifecycle, tc.lifecycle)
		}
		for _, op := range []string{catalog.OpStart, catalog.OpResume, catalog.OpIntegrationCheck, catalog.OpRelease} {
			if !e.Supports(op) {
				t.Errorf("%s does not declare %s", tc.name, op)
			}
		}
	}
	if _, err := s.engine("missing"); err == nil || !strings.Contains(err.Error(), "select one installed engine: no, onto, to") {
		t.Fatalf("unknown engine error = %v", err)
	}
	// A stateless engine takes no lifecycle change, and a native one needs it.
	if _, err := s.Create(context.Background(), "x", "no", "change", "existing", []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "no has no lifecycle change") {
		t.Fatalf("stateless engine with a change: %v", err)
	}
	if _, err := s.Create(context.Background(), "x", "onto", "", "existing", []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "requires its existing active --change") {
		t.Fatalf("native engine without a change: %v", err)
	}
}

func TestCreateRecordsTheEngineLifecycle(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "worktree")
	if task.Lifecycle != catalog.LifecycleStateless {
		t.Fatalf("lifecycle = %q", task.Lifecycle)
	}
	loaded, err := s.Load(context.Background(), task.Name)
	if err != nil || loaded.Lifecycle != catalog.LifecycleStateless {
		t.Fatalf("reload: %+v %v", loaded, err)
	}
}

func TestResumeRefusesAnEngineWhoseLifecycleChanged(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "existing")
	path := filepath.Join(s.Layout.ConfigRoot, ".homonto", "workspace-tasks", task.Name, "task.json")
	rewriteTask(t, path, func(m map[string]any) { m["lifecycle"] = catalog.LifecycleNative })
	if _, err := s.Load(context.Background(), task.Name); err == nil || !strings.Contains(err.Error(), "changed its lifecycle from native to stateless") {
		t.Fatalf("err = %v", err)
	}
	// A task recorded before descriptors adopts the current one.
	rewriteTask(t, path, func(m map[string]any) { delete(m, "lifecycle") })
	loaded, err := s.Load(context.Background(), task.Name)
	if err != nil || loaded.Lifecycle != catalog.LifecycleStateless {
		t.Fatalf("pre-descriptor task: %+v %v", loaded, err)
	}
}

func rewriteTask(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	if data, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// withLocalEngine installs a local engine package "ext" beside the fixture's
// builtins and reopens the store.
func withLocalEngine(t *testing.T, s *Store, engineTable string) *Store {
	t.Helper()
	pkg := filepath.Join(filepath.Dir(s.Layout.ConfigPath), "ext")
	put(t, filepath.Join(pkg, "skills", "ext", "SKILL.md"), "---\nname: ext\n---\next")
	put(t, filepath.Join(pkg, "framework.toml"), "manifest_schema = 2\nname = \"ext\"\nkind = \"engine\"\nversion = \"0.1.0\"\n"+engineTable+"[skills]\next = \"skills/ext\"\n")
	data, err := os.ReadFile(s.Layout.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	put(t, s.Layout.ConfigPath, string(data)+"[engines.ext]\nsource='local:./ext'\nscope='project'\n")
	reopened, err := Open(s.Layout.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return reopened
}

func TestExternalEngineSelection(t *testing.T) {
	t.Run("a stateless engine runs tasks", func(t *testing.T) {
		s := withLocalEngine(t, fixture(t), "[engine]\nlifecycle = \"stateless\"\nentry = [\"ext\"]\noperations = [\"start\", \"resume\", \"integration-check\", \"release\"]\nstages = [\"work\"]\n")
		task, err := s.Create(context.Background(), "feature", "ext", "", "worktree", []string{"api"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if task.Lifecycle != catalog.LifecycleStateless || task.Checkouts["api"].Path == task.Checkouts["api"].Source {
			t.Fatalf("stateless external task did not get a task-owned worktree: %+v", task.Checkouts["api"])
		}
		if _, err := s.Cleanup(context.Background(), task.Name, true); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("undeclared operations are refused", func(t *testing.T) {
		s := withLocalEngine(t, fixture(t), "[engine]\nlifecycle = \"stateless\"\nentry = [\"ext\"]\noperations = [\"start\", \"resume\"]\nstages = [\"work\"]\n")
		task, err := s.Create(context.Background(), "feature", "ext", "", "existing", []string{"api"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RunCheck(context.Background(), task, "pair", nil); err == nil || !strings.Contains(err.Error(), `engine "ext" does not support integration-check`) {
			t.Fatalf("check: %v", err)
		}
		if _, err := s.Cleanup(context.Background(), task.Name, true); err == nil || !strings.Contains(err.Error(), "does not support release") {
			t.Fatalf("cleanup: %v", err)
		}
	})
	t.Run("a native lifecycle engine waits for #67", func(t *testing.T) {
		s := withLocalEngine(t, fixture(t), "[engine]\nlifecycle = \"native\"\nbinary = \"ext\"\nentry = [\"ext\"]\noperations = [\"start\", \"resume\"]\nstages = [\"work\"]\n")
		if _, err := s.Create(context.Background(), "feature", "ext", "c", "existing", []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "only builtin lifecycle engines") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a legacy manifest carries no engine contract", func(t *testing.T) {
		s := fixture(t)
		pkg := filepath.Join(filepath.Dir(s.Layout.ConfigPath), "ext")
		put(t, filepath.Join(pkg, "skills", "ext", "SKILL.md"), "---\nname: ext\n---\next")
		put(t, filepath.Join(pkg, "framework.toml"), "name = \"ext\"\nkind = \"engine\"\nversion = \"0.1.0\"\n[skills]\next = \"skills/ext\"\n")
		data, _ := os.ReadFile(s.Layout.ConfigPath)
		put(t, s.Layout.ConfigPath, string(data)+"[engines.ext]\nsource='local:./ext'\nscope='project'\n")
		s, err := Open(s.Layout.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Create(context.Background(), "feature", "ext", "", "existing", []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "has no schema 2 [engine] contract") {
			t.Fatalf("err = %v", err)
		}
	})
}
