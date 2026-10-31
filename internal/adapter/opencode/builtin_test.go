package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/adapter"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/secret"
	"github.com/Novirmor/homonto/internal/state"
)

func builtinCfg() *config.Config {
	return &config.Config{
		Skills: map[string]config.Resource{
			"brainstorming": {Source: "builtin:brainstorming", Scope: "user", Targets: []string{"opencode"}},
		},
	}
}

func resolver() *secret.Resolver {
	return &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
}

func TestBuiltinSkillLinksToCatalogRoot(t *testing.T) {
	home := t.TempDir()
	catalogRoot := t.TempDir()
	os.MkdirAll(filepath.Join(catalogRoot, "brainstorming"), 0o755)
	a := New(home, t.TempDir()).WithCatalogRoot(catalogRoot)
	st, _ := state.Load(t.TempDir())

	cs, err := a.Plan(builtinCfg(), st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(builtinCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dst := filepath.Join(home, ".config", "opencode", "skills", "brainstorming")
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("skill link missing: %v", err)
	}
	if want := filepath.Join(catalogRoot, "brainstorming"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
	if _, ok := st.Get("opencode", "skill.brainstorming"); !ok {
		t.Fatal("skill.brainstorming not recorded")
	}
	cs2, _ := a.Plan(builtinCfg(), st)
	for _, c := range cs2.Changes {
		if c.Key == "skill.brainstorming" && c.Action != "noop" {
			t.Fatalf("re-plan not idempotent: %+v", c)
		}
	}
}

func TestBuiltinSkillPrunedWhenDeDeclared(t *testing.T) {
	home := t.TempDir()
	catalogRoot := t.TempDir()
	os.MkdirAll(filepath.Join(catalogRoot, "brainstorming"), 0o755)
	a := New(home, t.TempDir()).WithCatalogRoot(catalogRoot)
	st, _ := state.Load(t.TempDir())
	cs, _ := a.Plan(builtinCfg(), st)
	if err := a.Apply(builtinCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dst := filepath.Join(home, ".config", "opencode", "skills", "brainstorming")
	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatalf("plan empty: %v", err)
	}
	if err := a.Apply(&config.Config{}, cs2, resolver(), st); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("de-declared builtin skill link not pruned")
	}
}

func TestBuiltinSkillConflictNotClobbered(t *testing.T) {
	home := t.TempDir()
	catalogRoot := t.TempDir()
	os.MkdirAll(filepath.Join(catalogRoot, "brainstorming"), 0o755)
	a := New(home, t.TempDir()).WithCatalogRoot(catalogRoot)
	st, _ := state.Load(t.TempDir())
	dst := filepath.Join(home, ".config", "opencode", "skills", "brainstorming")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("user file"), 0o644)
	if _, err := a.Plan(builtinCfg(), st); err == nil {
		t.Fatal("expected conflict for real file at builtin skill link dst")
	}
	if b, _ := os.ReadFile(dst); string(b) != "user file" {
		t.Fatal("conflict clobbered the user file")
	}
}

func builtinCmdCfg() *config.Config {
	return &config.Config{
		Commands: map[string]config.Resource{
			"example-command": {Source: "builtin:example-command", Scope: "user", Targets: []string{"opencode"}},
		},
	}
}

func TestBuiltinCommandLinksToCommandCatalogRoot(t *testing.T) {
	home := t.TempDir()
	cmdRoot := t.TempDir()
	os.WriteFile(filepath.Join(cmdRoot, "example-command.md"), []byte("body"), 0o644)

	a := New(home, t.TempDir()).WithCommandCatalogRoot(cmdRoot)
	st, _ := state.Load(t.TempDir())
	cs, err := a.Plan(builtinCmdCfg(), st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(builtinCmdCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	dst := filepath.Join(home, ".config", "opencode", "command", "example-command.md")
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("command link missing: %v", err)
	}
	if want := filepath.Join(cmdRoot, "example-command.md"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
	if _, ok := st.Get("opencode", "command.example-command"); !ok {
		t.Fatal("command.example-command not recorded in state")
	}
	cs2, _ := a.Plan(builtinCmdCfg(), st)
	for _, c := range cs2.Changes {
		if c.Key == "command.example-command" && c.Action != "noop" {
			t.Fatalf("re-plan not idempotent: %+v", c)
		}
	}
}

func TestBuiltinCommandPrunedWhenDeDeclared(t *testing.T) {
	home := t.TempDir()
	cmdRoot := t.TempDir()
	os.WriteFile(filepath.Join(cmdRoot, "example-command.md"), []byte("body"), 0o644)
	a := New(home, t.TempDir()).WithCommandCatalogRoot(cmdRoot)
	st, _ := state.Load(t.TempDir())

	cs, _ := a.Plan(builtinCmdCfg(), st)
	if err := a.Apply(builtinCmdCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dst := filepath.Join(home, ".config", "opencode", "command", "example-command.md")

	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatalf("plan empty: %v", err)
	}
	if err := a.Apply(&config.Config{}, cs2, resolver(), st); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("de-declared builtin command link not pruned")
	}
}

func TestBuiltinCommandConflictNotClobbered(t *testing.T) {
	home := t.TempDir()
	cmdRoot := t.TempDir()
	os.WriteFile(filepath.Join(cmdRoot, "example-command.md"), []byte("body"), 0o644)
	a := New(home, t.TempDir()).WithCommandCatalogRoot(cmdRoot)
	st, _ := state.Load(t.TempDir())

	dst := filepath.Join(home, ".config", "opencode", "command", "example-command.md")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("user file"), 0o644)

	if _, err := a.Plan(builtinCmdCfg(), st); err == nil {
		t.Fatal("expected conflict for real file at builtin command link dst")
	}
	if b, _ := os.ReadFile(dst); string(b) != "user file" {
		t.Fatal("conflict clobbered the user file")
	}

	// Apply must fail fast on the same command conflict BEFORE writing any
	// file — not just Plan. a.commands is already populated by the Plan call
	// above (set before its own conflict check), so build a ChangeSet with an
	// unrelated setting write and confirm Apply rejects it without touching
	// opencode.jsonc: a partial write (opencode.jsonc written, then erroring on
	// the command link) would break the "fail fast before any write" invariant.
	cs := adapter.ChangeSet{Tool: "opencode", Changes: []adapter.Change{
		{Action: "create", Key: "setting.foo", New: `"bar"`},
	}}
	if err := a.Apply(builtinCmdCfg(), cs, resolver(), st); err == nil {
		t.Fatal("expected Apply to fail fast on the command link conflict")
	}
	if _, err := os.Stat(a.cfgFile()); !os.IsNotExist(err) {
		t.Fatal("Apply wrote opencode.jsonc before failing on the command link conflict")
	}
}

func builtinSubagentCfg() *config.Config {
	return &config.Config{
		Subagents: map[string]config.Subagent{
			"code-reviewer": {Source: "builtin:code-reviewer", Scope: "user", Targets: []string{"opencode"}},
		},
	}
}

func TestBuiltinSubagentLinksToSubagentCatalogRoot(t *testing.T) {
	home := t.TempDir()
	saRoot := t.TempDir()
	os.WriteFile(filepath.Join(saRoot, "code-reviewer.md"), []byte("body"), 0o644)

	a := New(home, t.TempDir()).WithSubagentCatalogRoot(saRoot)
	st, _ := state.Load(t.TempDir())
	cs, err := a.Plan(builtinSubagentCfg(), st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(builtinSubagentCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	dst := filepath.Join(home, ".config", "opencode", "agent", "code-reviewer.md")
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("subagent link missing: %v", err)
	}
	if want := filepath.Join(saRoot, "code-reviewer.md"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
	if _, ok := st.Get("opencode", "subagent.code-reviewer"); !ok {
		t.Fatal("subagent.code-reviewer not recorded in state")
	}
	cs2, _ := a.Plan(builtinSubagentCfg(), st)
	for _, c := range cs2.Changes {
		if c.Key == "subagent.code-reviewer" && c.Action != "noop" {
			t.Fatalf("re-plan not idempotent: %+v", c)
		}
	}
}

func TestBuiltinSubagentPrunedWhenDeDeclared(t *testing.T) {
	home := t.TempDir()
	saRoot := t.TempDir()
	os.WriteFile(filepath.Join(saRoot, "code-reviewer.md"), []byte("body"), 0o644)
	a := New(home, t.TempDir()).WithSubagentCatalogRoot(saRoot)
	st, _ := state.Load(t.TempDir())
	cs, _ := a.Plan(builtinSubagentCfg(), st)
	if err := a.Apply(builtinSubagentCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dst := filepath.Join(home, ".config", "opencode", "agent", "code-reviewer.md")
	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatalf("plan empty: %v", err)
	}
	if err := a.Apply(&config.Config{}, cs2, resolver(), st); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("de-declared builtin subagent link not pruned")
	}
}

func TestBuiltinSubagentConflictNotClobbered(t *testing.T) {
	home := t.TempDir()
	saRoot := t.TempDir()
	os.WriteFile(filepath.Join(saRoot, "code-reviewer.md"), []byte("body"), 0o644)
	dst := filepath.Join(home, ".config", "opencode", "agent", "code-reviewer.md")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(dst, []byte("REAL USER FILE"), 0o644)

	a := New(home, t.TempDir()).WithSubagentCatalogRoot(saRoot)
	st, _ := state.Load(t.TempDir())
	cs, _ := a.Plan(builtinSubagentCfg(), st)
	if err := a.Apply(builtinSubagentCfg(), cs, resolver(), st); err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "REAL USER FILE" {
		t.Fatal("conflicting real file was clobbered")
	}
}

func TestBuiltinSubagentAdoptsExistingLink(t *testing.T) {
	home := t.TempDir()
	saRoot := t.TempDir()
	src := filepath.Join(saRoot, "code-reviewer.md")
	os.WriteFile(src, []byte("body"), 0o644)
	dst := filepath.Join(home, ".config", "opencode", "agent", "code-reviewer.md")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
	a := New(home, t.TempDir()).WithSubagentCatalogRoot(saRoot)
	st, _ := state.Load(t.TempDir())
	cs, _ := a.Plan(builtinSubagentCfg(), st)
	adopted := false
	for _, c := range cs.Changes {
		if c.Key == "subagent.code-reviewer" && c.Action == "adopt" {
			adopted = true
		}
	}
	if !adopted {
		t.Fatalf("pre-existing correct link not adopted: %+v", cs.Changes)
	}
	if err := a.Apply(builtinSubagentCfg(), cs, resolver(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if tgt, _ := os.Readlink(dst); tgt != src {
		t.Fatal("adopt must leave the on-disk link untouched")
	}
}
