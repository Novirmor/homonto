package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_RejectsUnsupportedPackageSource: a package source that is neither
// builtin: nor local: (a bare name, or a remote: URL) expands nothing and is
// rejected loudly at load (F35). local: frameworks are accepted post-E1 — see
// TestLoad_AcceptsLocalFramework.
func TestLoad_RejectsUnsupportedPackageSource(t *testing.T) {
	for _, src := range []string{"onto", "remote:https://example.com/onto"} {
		p := filepath.Join(t.TempDir(), "homonto.toml")
		if err := os.WriteFile(p, []byte("[engines.onto]\nsource = \""+src+"\"\nscope = \"user\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil {
			t.Fatalf("package source %q accepted, want a load error", src)
		}
		if !strings.Contains(err.Error(), "onto") {
			t.Errorf("error %q should name the package", err.Error())
		}
	}
}

// TestLoad_AcceptsLocalPackage: an [engines.X] source="local:<path>" is
// accepted at load post-E1. Validation checks only the source shape; the local
// framework root is resolved later at expansion/materialization time.
func TestLoad_AcceptsLocalPackage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte("[engines.myfw]\nsource = \"local:./myfw\"\nscope = \"user\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("local: package rejected at load: %v", err)
	}
}

func TestLoad_AcceptsBuiltinEngine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[engines.onto]
source = "builtin:onto"
scope = "user"
`+ontoFrameworkModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("builtin: engine rejected at load: %v", err)
	}
}

// TestLoad_AcceptsOntoAndToTogether verifies that both engines may be installed.
func TestLoad_AcceptsOntoAndToTogether(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[engines.onto]
source = "builtin:onto"
scope = "project"
[engines.to]
source = "builtin:to"
scope = "project"
`+ontoFrameworkModels()+toSpecialistsOnly()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("onto+to rejected at load: %v", err)
	}
	if _, ok := cfg.Frameworks["onto"]; !ok {
		t.Error("onto engine missing from loaded config")
	}
	if _, ok := cfg.Frameworks["to"]; !ok {
		t.Error("to engine missing from loaded config")
	}
}

// toFrameworkModels is the per-agent override blocks required by the to
// engine's five expanded subagents (the shared homonto primary plus four
// specialists). toSpecialistsOnly omits the primary for configs that already
// carry ontoFrameworkModels() — TOML rejects the duplicate table, and the
// shared primary needs exactly one block.
func toFrameworkModels() string {
	return modelsFor("homonto", "to-explorer", "to-implementer", "to-reviewer", "to-skeptic")
}

func toSpecialistsOnly() string {
	return modelsFor("to-explorer", "to-implementer", "to-reviewer", "to-skeptic")
}

// TestLoad_AcceptsToAlone: [engines.to] on its own is a valid builtin
// engine declaration.
func TestLoad_AcceptsToAlone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[engines.to]
source = "builtin:to"
scope = "project"
`+toFrameworkModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("to engine rejected at load: %v", err)
	}
}

func TestLoad_AcceptsStandaloneHWorkflow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[workflows.h]
source = "builtin:h"
scope = "project"
`+modelsFor("homonto", "h-spike", "h-review")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("standalone h workflow rejected at load: %v", err)
	}
	if _, ok := cfg.Workflows["h"]; !ok {
		t.Fatal("h workflow missing from loaded config")
	}
	if len(cfg.Engines) != 0 {
		t.Fatalf("h-only config unexpectedly has engines: %#v", cfg.Engines)
	}
}

func TestLoad_AcceptsNoEngine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[engines.no]
source = "builtin:no"
scope = "project"
`+modelsFor("no-implementer", "no-reviewer")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("no engine rejected at load: %v", err)
	}
	if _, ok := cfg.Engines["no"]; !ok {
		t.Fatal("no engine missing from loaded config")
	}
}

func TestLoad_RejectsLegacyFrameworkDeclaration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[frameworks.onto]
source = "builtin:onto"
scope = "project"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "frameworks") || !strings.Contains(err.Error(), "unknown configuration fields") {
		t.Fatalf("legacy declaration error = %v, want strict rejection of [frameworks.*]", err)
	}
}

func TestLoad_RejectsBuiltinPackageKindMismatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"workflow as engine", "[engines.h]\nsource = \"builtin:h\"", "a workflow, not an engine"},
		{"engine as workflow", "[workflows.onto]\nsource = \"builtin:onto\"", "an engine, not a workflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "homonto.toml")
			if err := os.WriteFile(p, []byte(tc.text+"\nscope = \"project\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExpansionValidatesLocalAndRemotePackageKinds(t *testing.T) {
	for _, sourceType := range []string{"local", "remote"} {
		for _, namespace := range []string{"workflows", "engines"} {
			for _, kind := range []string{"workflow", "engine", ""} {
				t.Run(sourceType+"/"+namespace+"/"+kind, func(t *testing.T) {
					root := t.TempDir()
					pkg := filepath.Join(root, "package")
					if err := os.MkdirAll(filepath.Join(pkg, "skills", "probe"), 0o755); err != nil {
						t.Fatal(err)
					}
					manifest := fmt.Sprintf("name = 'demo'\nkind = %q\nversion = '0.1.0'\n[skills]\nprobe = 'skills/probe'\n", kind)
					for path, text := range map[string]string{
						"framework.toml":        manifest,
						"skills/probe/SKILL.md": "Probe skill.\n",
					} {
						if err := os.WriteFile(filepath.Join(pkg, path), []byte(text), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					source, pin := "local:./package", ""
					if sourceType == "remote" {
						source = "remote:https://example.invalid/package.tar.gz"
						pin = "digest = 'sha256:" + strings.Repeat("a", 64) + "'\n"
					}
					path := filepath.Join(root, "homonto.toml")
					text := fmt.Sprintf("[%s.demo]\nsource = %q\nscope = 'project'\n%s", namespace, source, pin)
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
					c, err := Load(path)
					if err != nil {
						t.Fatal(err)
					}
					if sourceType == "remote" {
						// The resolver supplies the verified cache root before expansion.
						c.SetRemoteFrameworkDirs(map[string]string{"demo": pkg})
					}
					entries, err := c.ExpandedSkillEntriesForTool("opencode")
					if kind != "" && kind+"s" != namespace {
						if err == nil || !strings.Contains(err.Error(), namespace+".demo") || !strings.Contains(err.Error(), "not "+article(strings.TrimSuffix(namespace, "s"))) {
							t.Fatalf("kind mismatch accepted or misreported: %v", err)
						}
					} else if err != nil || len(entries) != 1 || entries[0].Name != "probe" {
						t.Fatalf("matching or legacy package failed: %+v, %v", entries, err)
					}
				})
			}
		}
	}
}

func TestLoad_AllEnginesMayBeInstalled(t *testing.T) {
	var text string
	for _, engine := range []string{"no", "onto", "to"} {
		text += fmt.Sprintf("[engines.%s]\nsource = \"builtin:%s\"\nscope = \"project\"\n", engine, engine)
	}
	text += ontoFrameworkModels() + toSpecialistsOnly() + modelsFor("no-implementer", "no-reviewer")
	if _, err := Load(writeConfig(t, text)); err != nil {
		t.Fatalf("all three engines must coexist at installation: %v", err)
	}
}
