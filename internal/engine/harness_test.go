package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/adapter"
	"github.com/Novirmor/homonto/internal/capability"
)

// localEngineRequiring writes a schema 2 local engine requiring harness.
func localEngineRequiring(t *testing.T, repo, harness string) {
	t.Helper()
	fw := filepath.Join(repo, "myfw")
	if err := os.MkdirAll(filepath.Join(fw, "skills", "myskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	man := "manifest_schema = 2\nname = \"myfw\"\nkind = \"engine\"\nversion = \"0.1.0\"\n[compat]\ncontract = \"1.0\"\n" +
		"[engine]\nlifecycle = \"stateless\"\nentry = [\"myskill\"]\noperations = [\"start\", \"resume\"]\nstages = [\"work\"]\n" +
		"[requires]\nharness = [" + harness + "]\n[skills]\nmyskill = \"skills/myskill\"\n"
	for path, data := range map[string]string{
		filepath.Join(fw, "framework.toml"):                man,
		filepath.Join(fw, "skills", "myskill", "SKILL.md"): "---\nname: myskill\n---\ns",
		filepath.Join(repo, "homonto.toml"):                "[engines.myfw]\nsource = \"local:./myfw\"\nscope = \"user\"\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// limitedAdapter is a harness whose declaration lacks context hooks and only
// instructs agent tool limits.
type limitedAdapter struct{ adapter.Adapter }

func (limitedAdapter) Capabilities() capability.Declaration {
	return capability.Declaration{Harness: "opencode", Version: "test", Provides: map[capability.Name]capability.Level{
		capability.Skills:          capability.Enforced,
		capability.AgentToolLimits: capability.InstructionOnly,
	}}
}

// undeclaredAdapter declares no capabilities at all.
type undeclaredAdapter struct{ adapter.Adapter }

func TestPlanRequiresDeclaredHarnessCapabilities(t *testing.T) {
	const required = `"skills", "context-hooks", "agent-tool-limits"`
	t.Run("opencode provides them", func(t *testing.T) {
		repo := t.TempDir()
		localEngineRequiring(t, repo, required)
		if _, err := buildEngine(t, t.TempDir(), repo).Plan(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a declaration with gaps fails closed naming them", func(t *testing.T) {
		repo := t.TempDir()
		localEngineRequiring(t, repo, required)
		e := buildEngine(t, t.TempDir(), repo)
		e.Adapters = []adapter.Adapter{limitedAdapter{e.Adapters[0]}}
		_, err := e.Plan()
		if err == nil {
			t.Fatal("plan must fail on a missing capability")
		}
		for _, want := range []string{`package "myfw"`, "context-hooks", "agent-tool-limits (instruction-only only)", "opencode test"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})
	t.Run("an adapter without a declaration provides nothing", func(t *testing.T) {
		repo := t.TempDir()
		localEngineRequiring(t, repo, `"skills"`)
		e := buildEngine(t, t.TempDir(), repo)
		e.Adapters = []adapter.Adapter{undeclaredAdapter{e.Adapters[0]}}
		if _, err := e.Plan(); err == nil || !strings.Contains(err.Error(), "skills") {
			t.Fatalf("err = %v", err)
		}
	})
}
