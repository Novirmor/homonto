package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/catalog"
	"github.com/noviopenworks/homonto/internal/workspace"
	"gopkg.in/yaml.v3"
)

func TestWorkspaceProjectionAndPermissions(t *testing.T) {
	for _, mode := range []string{"existing", "managed", "existing-in-source", "existing-in-config"} {
		t.Run(mode, func(t *testing.T) {
			base, home := t.TempDir(), t.TempDir()
			root := filepath.Join(base, "config home")
			sources := []string{filepath.Join(base, "source-a"), filepath.Join(base, "source-b")}
			records, parent := filepath.Join(base, "records"), filepath.Join(base, "worktrees")
			for _, dir := range append([]string{root}, sources...) {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range sources {
				if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
					t.Fatalf("git init: %v: %s", err, out)
				}
			}
			gitMode := mode
			if mode == "existing-in-source" {
				gitMode, records = "existing", filepath.Join(sources[0], "records")
			}
			if mode == "existing-in-config" {
				gitMode, records = "existing", filepath.Join(root, "records")
			}
			configPath := filepath.Join(root, "workspace.toml")
			write := func(parent string) {
				t.Helper()
				doc := fmt.Sprintf("schema_version = 2\n[workflow]\nroot = %q\ngit = %q\n[repos]\na = %q\nb = %q\n", records, gitMode, sources[0], sources[1]) + hFrameworkTOML
				if parent != "" {
					doc += fmt.Sprintf("\n[worktrees]\ndir = %q\n", parent)
				}
				if err := os.WriteFile(configPath, []byte(doc), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			build := func() *Engine {
				t.Helper()
				e, err := Build(context.Background(), configPath, home, "homonto")
				if err != nil {
					t.Fatal(err)
				}
				return e
			}
			apply := func(e *Engine) {
				t.Helper()
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				if e.CatalogNeedsMaterialize() {
					t.Fatal("apply did not converge")
				}
			}
			checkPermissions := func(e *Engine, parent, oldParent string) {
				t.Helper()
				for _, name := range []string{"homonto", "onto-implementer", "to-implementer", "onto-reviewer", "to-explorer", "h-review"} {
					data, err := os.ReadFile(filepath.Join(e.SubagentDir(), name+".opencode.md"))
					if err != nil {
						t.Fatal(err)
					}
					var fm struct {
						Permission map[string]any `yaml:"permission"`
					}
					if err := yaml.Unmarshal(bytes.Split(data, []byte("---\n"))[1], &fm); err != nil {
						t.Fatal(err)
					}
					if name == "onto-reviewer" || name == "to-explorer" || name == "h-review" {
						if fm.Permission["edit"] != "deny" || fm.Permission["bash"] != "deny" || fm.Permission["external_directory"] != nil {
							t.Errorf("read-only %s permissions = %#v", name, fm.Permission)
						}
						continue
					}
					dirs, ok := fm.Permission["external_directory"].(map[string]any)
					if !ok || dirs["*"] != "deny" {
						t.Fatalf("%s must deny undeclared directories: %#v", name, fm.Permission)
					}
					for _, source := range sources {
						if dirs[filepath.ToSlash(filepath.Join(source, "**"))] != "allow" {
							t.Errorf("%s missing source grant %s", name, source)
						}
					}
					wantRecords := "deny"
					if name == "homonto" {
						wantRecords = "allow"
					}
					if dirs[filepath.ToSlash(filepath.Join(records, "**"))] != wantRecords {
						t.Errorf("%s workflow rule = %#v, want %s", name, dirs, wantRecords)
					}
					if name != "homonto" {
						edits, ok := fm.Permission["edit"].(map[string]any)
						if !ok || edits[filepath.ToSlash(filepath.Join(records, "**"))] != "deny" {
							t.Errorf("%s native edit can write workflow records inside a host workspace", name)
						}
						// These agents are installed at the non-Git control root, not
						// in either source checkout. OpenCode's worktree is "/".
						for _, host := range []string{string(filepath.Separator)} {
							rel, err := filepath.Rel(host, records)
							if err != nil {
								t.Fatal(err)
							}
							if edits[filepath.ToSlash(filepath.Join(rel, "**"))] != "deny" {
								t.Errorf("%s missing host-relative record deny from %s", name, host)
							}
						}
					}
					if parent != "" {
						for _, alias := range []string{"a", "b"} {
							if dirs[filepath.ToSlash(filepath.Join(parent, alias, "**"))] != "allow" {
								t.Errorf("%s missing bounded worktree grant for %s", name, alias)
							}
						}
						if dirs[filepath.ToSlash(filepath.Join(parent, "**"))] != nil {
							t.Errorf("%s granted entire worktrees parent", name)
						}
					}
					for pattern := range dirs {
						if oldParent != "" && strings.HasPrefix(pattern, oldParent+"/") {
							t.Errorf("%s retained old worktrees grant %s", name, pattern)
						}
						if pattern == filepath.ToSlash(filepath.Join(root, "**")) {
							t.Errorf("%s granted implicit config source: %s", name, pattern)
						}
					}
				}
			}
			write(parent)
			e := build()
			apply(e)
			checkPermissions(e, parent, "")
			for _, skill := range []string{"homonto", "onto", "to"} {
				ref := filepath.Join(e.CatalogDir(), skill, catalog.WorkspaceReferencePath)
				data, err := os.ReadFile(ref)
				if err != nil || !bytes.Equal(data, catalog.RenderWorkspace(e.WorkspaceLayout)) {
					t.Fatalf("%s reference differs from resolved layout: %v", skill, err)
				}
				if !strings.Contains(string(data), "--config '"+configPath+"'") {
					t.Fatal("reference lost actual config filename")
				}
				info, err := os.Stat(ref)
				if err != nil {
					t.Fatal(err)
				}
				apply(build())
				after, err := os.Stat(ref)
				if err != nil || !os.SameFile(info, after) || info.ModTime() != after.ModTime() {
					t.Fatalf("idempotent apply rewrote %s: %v", skill, err)
				}
				if err := os.Remove(ref); err != nil {
					t.Fatal(err)
				}
				repair := build()
				if !repair.CatalogNeedsMaterialize() {
					t.Fatal("missing reference not detected")
				}
				apply(repair)
				data, err = os.ReadFile(ref)
				if err != nil || !bytes.Equal(data, catalog.RenderWorkspace(e.WorkspaceLayout)) {
					t.Fatalf("reference repair failed: %v", err)
				}
			}
			previous := e.State.RenderFingerprintRecorded()
			newParent := filepath.Join(base, "new worktrees")
			write(newParent)
			e = build()
			if !e.CatalogNeedsMaterialize() {
				t.Fatal("parent change did not invalidate fingerprint")
			}
			apply(e)
			if e.State.RenderFingerprintRecorded() == previous {
				t.Fatal("parent absent from fingerprint")
			}
			checkPermissions(e, newParent, parent)
			write("")
			e = build()
			apply(e)
			checkPermissions(e, "", newParent)
			for _, path := range []string{filepath.Join(root, "homonto.toml"), filepath.Join(root, ".git"), parent, newParent, records} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("projection must not initialize config/source/workflow/allocation state at %s: %v", path, err)
				}
			}
		})
	}
}

func TestWorkspaceFingerprintAndLegacyReferenceRemoval(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(ontoFrameworkTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, root)
	fingerprint := func() string {
		t.Helper()
		p, err := e.planCatalog()
		if err != nil {
			t.Fatal(err)
		}
		return p.fingerprint
	}
	legacy := fingerprint()
	layout := workspace.Layout{SchemaVersion: 2, ConfigPath: filepath.Join(root, "homonto.toml"), ConfigRoot: root, WorkflowRoot: filepath.Join(root, "records"), GitMode: "existing", Repos: map[string]string{"a": "/source/a"}}
	e.WorkspaceLayout = layout
	baseline := fingerprint()
	if baseline == legacy {
		t.Fatal("schema 2 did not change fingerprint")
	}
	for _, edit := range []func(*workspace.Layout){
		func(l *workspace.Layout) { l.ConfigPath = filepath.Join(root, "custom.toml") },
		func(l *workspace.Layout) { l.ConfigRoot = "/other/config" },
		func(l *workspace.Layout) { l.WorkflowRoot = filepath.Join(root, "other-records") },
		func(l *workspace.Layout) { l.GitMode = "managed" },
		func(l *workspace.Layout) { l.WorktreesDir = "/execution" },
		func(l *workspace.Layout) { l.Repos = map[string]string{"a": "/source/other"} },
		func(l *workspace.Layout) { l.Repos = map[string]string{"renamed": "/source/a"} },
	} {
		e.WorkspaceLayout = layout
		edit(&e.WorkspaceLayout)
		if fingerprint() == baseline {
			t.Fatal("resolved layout change absent from fingerprint")
		}
	}
	e.WorkspaceLayout = layout
	if fingerprint() != baseline {
		t.Fatal("layout fingerprint is not deterministic")
	}
	if err := e.materializeCatalog(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"homonto", "onto"} {
		if _, err := os.Stat(filepath.Join(e.CatalogDir(), name, catalog.WorkspaceReferencePath)); err != nil {
			t.Fatal(err)
		}
	}
	e.WorkspaceLayout = workspace.Layout{}
	if fingerprint() != legacy {
		t.Fatal("legacy fingerprint changed")
	}
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("reference withdrawal not detected")
	}
	if err := e.materializeCatalog(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"homonto", "onto"} {
		if _, err := os.Stat(filepath.Join(e.CatalogDir(), name, catalog.WorkspaceReferencePath)); !os.IsNotExist(err) {
			t.Errorf("legacy projection retained %s reference: %v", name, err)
		}
	}
}

func TestWorkspaceRejectsWildcardWorktreeNamespace(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", source).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	for _, alias := range []string{"*", "a?"} {
		configPath := filepath.Join(root, "workspace.toml")
		doc := fmt.Sprintf("schema_version = 2\n[repos]\n%q = %q\n[worktrees]\ndir = 'execution'\n", alias, source)
		if err := os.WriteFile(configPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Build(context.Background(), configPath, t.TempDir(), "homonto"); err == nil || !strings.Contains(err.Error(), "wildcard") {
			t.Fatalf("unsafe worktree namespace %q: %v", alias, err)
		}
	}
}
