package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/state"
)

func TestReleaseOwnLinksAfterRepoMove(t *testing.T) {
	for _, scope := range []string{"user", "project"} {
		t.Run(scope, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			proj := filepath.Join(root, "before")
			content := filepath.Join(proj, "homonto")
			if err := os.MkdirAll(filepath.Join(content, "skills", "foo"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(content, "commands"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(content, "commands", "bar.md"), []byte("body\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			a := New(home, content).WithProjectRoot(proj)
			st, err := state.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cfg := cfgWithSkills(scope, "foo")
			cfg.Commands = map[string]config.Resource{"bar": {Source: "local:bar", Scope: scope}}
			cs, err := a.Plan(cfg, st)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Apply(cfg, cs, noSecret(), st); err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			for key, path := range map[string]string{"skill.foo": filepath.Join(a.SkillsDir(scope), "foo"), "command.bar": filepath.Join(a.CommandsDir(scope), "bar.md")} {
				before[key], err = os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			moved := filepath.Join(root, "after")
			if err := os.Rename(proj, moved); err != nil {
				t.Fatal(err)
			}
			a = New(home, filepath.Join(moved, "homonto")).WithProjectRoot(moved)
			cs, err = a.Plan(&config.Config{}, st)
			if err != nil {
				t.Fatal(err)
			}
			for key := range before {
				ch := findChange(cs, "delete", key)
				if ch == nil || ch.Cause != adapter.CauseRelease {
					t.Fatalf("moved own link planned as removal instead of release: %+v", cs.Changes)
				}
			}
			if err := a.Apply(&config.Config{}, cs, noSecret(), st); err != nil {
				t.Fatal(err)
			}
			for key, path := range map[string]string{"skill.foo": filepath.Join(a.SkillsDir(scope), "foo"), "command.bar": filepath.Join(a.CommandsDir(scope), "bar.md")} {
				got, err := os.Readlink(path)
				if err != nil || got != before[key] {
					t.Fatalf("handover changed %s: %q, %v", key, got, err)
				}
				if _, ok := st.Get("opencode", key); ok {
					t.Fatalf("handover retained %s state", key)
				}
			}
		})
	}
}
