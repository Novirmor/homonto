package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/plan"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

// Two tools across two repositories (ADR 0066 M3): a repo-tagged MCP
// projecting to both tools lands in the repository's own opencode.jsonc AND
// .mcp.json, both tools' records share the ONE state.<repo>.json partition
// keyed by tool id, per-tool attribution appears in plan output, and the
// undeclared sibling repo is untouched by either tool.
func TestMultiRepoClaudeFanout(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	cfgRepo := filepath.Join(base, "cfg")
	svc := newGitWorktree(t, filepath.Join(base, "svc"))
	lib := newGitWorktree(t, filepath.Join(base, "lib"))
	content := filepath.Join(cfgRepo, "homonto")
	for _, d := range []string{cfgRepo, content} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	cfg := `
[repos]
svc = "../svc"
lib = "../lib"

[mcps.shared]
command = ["probe", "serve"]
scope = "project"
repo = "svc"
targets = ["opencode", "claude"]

[mcps.lib-only]
command = ["libtool"]
scope = "project"
repo = "lib"
targets = ["claude"]
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func() *Engine {
		e, err := Build(context.Background(), cfgPath, home, content)
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		return e
	}
	e := build()
	if len(e.RepoTargets) != 2 {
		t.Fatalf("RepoTargets = %d, want 2", len(e.RepoTargets))
	}
	for _, rt := range e.RepoTargets {
		tools := map[string]bool{}
		for _, a := range rt.Adapters {
			tools[a.Name()] = true
		}
		if _, ok := tools["opencode@"+rt.Name]; !ok {
			t.Fatalf("repo %s: opencode adapter missing: %v", rt.Name, tools)
		}
		if _, ok := tools["claude@"+rt.Name]; !ok {
			t.Fatalf("repo %s: claude adapter missing (config targets claude): %v", rt.Name, tools)
		}
	}

	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	wantTools := map[string]bool{
		"opencode":     false, // config-repo adapter: nothing declared there
		"claude":       false, // ditto
		"opencode@svc": false,
		"claude@svc":   false,
		"claude@lib":   false,
		"opencode@lib": false,
	}
	for _, cs := range sets {
		if _, ok := wantTools[cs.Tool]; ok {
			wantTools[cs.Tool] = true
		}
	}
	if !wantTools["opencode@svc"] || !wantTools["claude@svc"] || !wantTools["claude@lib"] {
		t.Fatalf("plan must carry per-tool repo changesets, saw %v", sets)
	}
	if wantTools["opencode@lib"] {
		// An empty opencode@lib changeset is fine (the adapter plans nothing);
		// only an actual change would be wrong. Checked by the file assertion
		// below: lib must have no opencode.jsonc.
		for _, cs := range sets {
			if cs.Tool == "opencode@lib" && len(cs.Changes) > 0 {
				t.Fatalf("lib declares a claude-only MCP; opencode@lib must plan nothing, got %+v", cs.Changes)
			}
		}
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// svc received BOTH tools' project files; lib only claude's.
	oc, err := os.ReadFile(filepath.Join(svc, "opencode.jsonc"))
	if err != nil || gjson.GetBytes(oc, "mcp.shared.command.0").String() != "probe" {
		t.Fatalf("svc opencode projection missing:\n%s\n%v", oc, err)
	}
	cj, err := os.ReadFile(filepath.Join(svc, ".mcp.json"))
	if err != nil || gjson.GetBytes(cj, "mcpServers.shared.command").String() != "probe" {
		t.Fatalf("svc claude projection missing:\n%s\n%v", cj, err)
	}
	lj, err := os.ReadFile(filepath.Join(lib, ".mcp.json"))
	if err != nil || gjson.GetBytes(lj, "mcpServers.lib-only.command").String() != "libtool" {
		t.Fatalf("lib claude projection missing:\n%s\n%v", lj, err)
	}
	if _, err := os.Stat(filepath.Join(lib, "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatal("a claude-only repo MCP must not create opencode.jsonc in that repo")
	}

	// Both tools' records live in the ONE svc partition, keyed by tool id.
	var svcState *state.State
	for _, rt := range e.RepoTargets {
		if rt.Name == "svc" {
			svcState = rt.State
		}
	}
	if svcState == nil {
		t.Fatal("svc partition not found")
	}
	opRecord, ok := svcState.Get("opencode", "projmcp.shared")
	if !ok {
		t.Fatal("svc partition must hold the opencode projmcp record")
	}
	clRecord, ok := svcState.Get("claude", "projmcp.shared")
	if !ok {
		t.Fatal("svc partition must hold the claude projmcp record")
	}
	// Provenance must be recorded under the base tool id (repo-mode labels
	// carry @<repo>, which the partition never keys on).
	if opRecord.LastEvent == nil || clRecord.LastEvent == nil {
		t.Fatalf("repo records must carry apply history, got opencode=%+v claude=%+v", opRecord.LastEvent, clRecord.LastEvent)
	}

	// Idempotent across a fresh build (shared partition reloads both tools').
	e2 := build()
	sets2, err := e2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges(sets2) {
		t.Fatalf("second apply not idempotent: %s", plan.Render(sets2))
	}
}

// Dropping the claude target from a repo-tagged dual-tool MCP prunes only the
// claude record and file entry; the opencode record and its file entry in the
// same repository survive. This is the per-tool ownership isolation M3 owes.
func TestMultiRepoTargetSwitchPrunesOnlyClaude(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	cfgRepo := filepath.Join(base, "cfg")
	svc := newGitWorktree(t, filepath.Join(base, "svc"))
	content := filepath.Join(cfgRepo, "homonto")
	for _, d := range []string{cfgRepo, content} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	writeCfg := func(targets string) {
		doc := `
[repos]
svc = "../svc"

[mcps.shared]
command = ["probe", "serve"]
scope = "project"
repo = "svc"
targets = [` + targets + `]
`
		if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	apply := func() []string {
		e, err := Build(context.Background(), cfgPath, home, content)
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		sets, err := e.Plan()
		if err != nil {
			t.Fatal(err)
		}
		var actions []string
		for _, cs := range sets {
			for _, c := range cs.Changes {
				actions = append(actions, string(c.Action)+" "+cs.Tool+" "+c.Key)
			}
		}
		if err := e.Apply(context.Background(), sets); err != nil {
			t.Fatal(err)
		}
		return actions
	}

	writeCfg(`"opencode", "claude"`)
	apply()

	writeCfg(`"opencode"`)
	actions := apply()
	sawClaudeDelete := false
	for _, a := range actions {
		if a == "delete claude@svc projmcp.shared" {
			sawClaudeDelete = true
		}
		if a == "delete opencode@svc projmcp.shared" {
			t.Fatalf("opencode record must survive the target switch: %v", actions)
		}
	}
	if !sawClaudeDelete {
		t.Fatalf("claude record must be pruned on the target switch: %v", actions)
	}

	// Disk state: svc's .mcp.json loses the server; opencode.jsonc keeps it.
	cj, err := os.ReadFile(filepath.Join(svc, ".mcp.json"))
	if err == nil && gjson.GetBytes(cj, "mcpServers.shared").Exists() {
		t.Fatalf("claude entry must be gone from svc/.mcp.json:\n%s", cj)
	}
	oc, err := os.ReadFile(filepath.Join(svc, "opencode.jsonc"))
	if err != nil || !gjson.GetBytes(oc, "mcp.shared").Exists() {
		t.Fatalf("opencode entry must remain:\n%s\n%v", oc, err)
	}

	// Partition: opencode record alive, claude record tombstoned away.
	st, err := state.LoadNamed(filepath.Join(cfgRepo, ".homonto"), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("opencode", "projmcp.shared"); !ok {
		t.Fatal("opencode partition record must survive")
	}
	if _, ok := st.Get("claude", "projmcp.shared"); ok {
		t.Fatal("claude partition record must be gone")
	}
}

// A declared repo resolving to the config repository itself (schema 2 permits
// ".") must not produce two state partitions owning the same project file:
// Build rejects the overlap before any mutation, naming the file and both
// owners. The same config without overlapping declarations builds clean —
// declaring the self-repo alone is harmless.
func TestConfigRepoSelfDestinationConflictRejected(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	// Schema 2 validates repos as real git worktrees (--show-toplevel), so
	// unlike the schema-0/1 helper this needs an actual repository.
	cfgRepo := filepath.Join(base, "cfg")
	if err := os.MkdirAll(cfgRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", cfgRepo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	content := filepath.Join(cfgRepo, "homonto")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	write := func(doc string) {
		if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := func() (*Engine, error) {
		return Build(context.Background(), cfgPath, home, content)
	}

	// Overlap: an untagged project MCP (main claude partition) and a
	// repo-tagged one (claude@self partition) both write <cfgRepo>/.mcp.json.
	write("schema_version = 2\n\n[repos]\nself = \".\"\n\n[mcps.main]\ncommand = [\"a\"]\nscope = \"project\"\ntargets = [\"claude\"]\n\n[mcps.tagged]\ncommand = [\"b\"]\nscope = \"project\"\nrepo = \"self\"\ntargets = [\"claude\"]\n")
	_, err := build()
	if err == nil {
		t.Fatal("overlapping destination ownership must fail the build")
	}
	msg := err.Error()
	for _, want := range []string{".mcp.json", "claude", "claude@self"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict error must name %q, got: %v", want, err)
		}
	}

	// The same self-repo without overlapping destinations builds fine.
	write("schema_version = 2\n\n[repos]\nself = \".\"\n\n[mcps.main]\ncommand = [\"a\"]\nscope = \"project\"\ntargets = [\"claude\"]\n")
	e, err := build()
	if err != nil {
		t.Fatalf("a self-repo without overlapping declarations must build: %v", err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply under a self-repo without overlap must succeed: %v", err)
	}

	// The declaration MOVE is the dangerous transition: retagging the applied
	// server to repo = "self" leaves the main partition holding a stale record
	// for the file the self-repo partition now declares. Build must refuse —
	// without the recorded pass, the two changesets would race (one adopts,
	// the other deletes) and a still-declared server would vanish.
	write("schema_version = 2\n\n[repos]\nself = \".\"\n\n[mcps.main]\ncommand = [\"a\"]\nscope = \"project\"\nrepo = \"self\"\ntargets = [\"claude\"]\n")
	if _, err := build(); err == nil {
		t.Fatal("retagging an applied server to the self-repo must fail closed")
	} else if msg := err.Error(); !strings.Contains(msg, "claude@self") || !strings.Contains(msg, "moved between partitions") {
		t.Fatalf("move conflict must name both owners and the way out, got: %v", err)
	}

	// The named way out works: delete the declaration and apply to clear the
	// stale record, then re-declare under the new owner.
	write("schema_version = 2\n\n[repos]\nself = \".\"\n")
	e, err = build()
	if err != nil {
		t.Fatalf("empty config must build during move cleanup: %v", err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("clearing the stale record must succeed: %v", err)
	}
	write("schema_version = 2\n\n[repos]\nself = \".\"\n\n[mcps.main]\ncommand = [\"a\"]\nscope = \"project\"\nrepo = \"self\"\ntargets = [\"claude\"]\n")
	e, err = build()
	if err != nil {
		t.Fatalf("re-declaring under the new owner must build after cleanup: %v", err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("re-declaring under the new owner must apply: %v", err)
	}
	if !gjson.GetBytes(mustReadFile(t, filepath.Join(cfgRepo, ".mcp.json")), "mcpServers.main").Exists() {
		t.Fatal("the re-declared server must be present after the move completes")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// The recorded-pass conflict detection covers LINK keys too: an applied
// project skill retagged to a self-repo leaves the main partition's record
// (whose Desired carries the link dst) claiming the same physical file the
// repo partition's declaration now links. Build must refuse — the structured
// and link namespaces get the same fail-closed move protection.
func TestSkillRetagToSelfRepoConflictRejected(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	cfgRepo := filepath.Join(base, "cfg")
	if err := os.MkdirAll(cfgRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", cfgRepo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	content := filepath.Join(cfgRepo, "homonto")
	writeRepoSkill(t, content, "demo")
	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	write := func(doc string) {
		if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := func() (*Engine, error) {
		return Build(context.Background(), cfgPath, home, content)
	}

	// Apply untagged: the main partition records the link with its dst.
	write("[skills.demo]\nsource = \"local:demo\"\nscope = \"project\"\n")
	e, err := build()
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("baseline skill apply: %v", err)
	}

	// Retag to the self-repo: the stale record and the new declaration now
	// claim one physical link destination across two partitions.
	write("schema_version = 2\n\n[repos]\nself = \".\"\n\n[skills.demo]\nsource = \"local:demo\"\nscope = \"project\"\nrepo = \"self\"\n")
	if _, err := build(); err == nil {
		t.Fatal("retagging an applied project skill to the self-repo must fail closed")
	} else if msg := err.Error(); !strings.Contains(msg, "skill") && !strings.Contains(msg, "moved between partitions") {
		// The message names the claimed destination and the resolution; the
		// exact label set may evolve, so accept either anchor.
		t.Fatalf("move conflict must explain the overlap and the way out, got: %v", err)
	}
}
