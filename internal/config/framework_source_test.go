package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_RejectsUnsupportedFrameworkSource: a framework source that is neither
// builtin: nor local: (a bare name, or a remote: URL) expands nothing and is
// rejected loudly at load (F35). local: frameworks are accepted post-E1 — see
// TestLoad_AcceptsLocalFramework.
func TestLoad_RejectsUnsupportedFrameworkSource(t *testing.T) {
	for _, src := range []string{"onto", "remote:https://example.com/onto"} {
		p := filepath.Join(t.TempDir(), "homonto.toml")
		if err := os.WriteFile(p, []byte("[frameworks.onto]\nsource = \""+src+"\"\nscope = \"user\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil {
			t.Fatalf("framework source %q accepted, want a load error", src)
		}
		if !strings.Contains(err.Error(), "onto") {
			t.Errorf("error %q should name the framework", err.Error())
		}
	}
}

// TestLoad_AcceptsLocalFramework: a [frameworks.X] source="local:<path>" is
// accepted at load post-E1. Validation checks only the source shape; the local
// framework root is resolved later at expansion/materialization time.
func TestLoad_AcceptsLocalFramework(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte("[frameworks.myfw]\nsource = \"local:./myfw\"\nscope = \"user\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("local: framework rejected at load: %v", err)
	}
}

func TestLoad_AcceptsBuiltinFramework(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[frameworks.onto]
source = "builtin:onto"
scope = "user"
`+ontoFrameworkModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("builtin: framework rejected at load: %v", err)
	}
}

// TestLoad_RejectsOntoAndToTogether: onto and to are an exclusive choice per
// repository (enterprise tooling vs. simple development), so a config that
// declares both frameworks fails at load.
func TestLoad_RejectsOntoAndToTogether(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[frameworks.onto]
source = "builtin:onto"
scope = "project"
[frameworks.to]
source = "builtin:to"
scope = "project"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("onto+to accepted, want a load error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should say the frameworks are mutually exclusive", err.Error())
	}
}

// toFrameworkModels is the per-agent override blocks required by the to
// framework's five expanded subagents (the to dispatcher plus four specialists).
func toFrameworkModels() string {
	return modelsFor("to", "to-explorer", "to-implementer", "to-reviewer", "to-skeptic")
}

// TestLoad_AcceptsToAlone: [frameworks.to] on its own is a valid builtin
// framework declaration.
func TestLoad_AcceptsToAlone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(`[frameworks.to]
source = "builtin:to"
scope = "project"
`+toFrameworkModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("to framework rejected at load: %v", err)
	}
}
