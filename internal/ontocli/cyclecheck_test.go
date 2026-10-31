package ontocli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Novirmor/homonto/internal/ontostate"
)

// graphJSONCycles mirrors graphJSON plus the cycles array.
type graphJSONCycles struct {
	Nodes  []graphNode `json:"nodes"`
	Edges  []graphEdge `json:"edges"`
	Cycles [][]string  `json:"cycles"`
}

func runGraph(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"graph"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestGraph_ReportsCycleInJSON(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/a", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "a", Phase: "build", Deps: []string{"b"}})
	mkState(t, root, "docs/changes/b", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "b", Phase: "build", Deps: []string{"a"}})

	out, err := runGraph(t, "--json", "--dir", root)
	if err != nil {
		t.Fatalf("graph --json: %v (%s)", err, out)
	}
	var g graphJSONCycles
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cycles) == 0 {
		t.Fatalf("expected a cycle, got none; out=%s", out)
	}
	// the cycle must mention both a and b
	seen := map[string]bool{}
	for _, cyc := range g.Cycles {
		for _, n := range cyc {
			seen[n] = true
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Errorf("cycle should mention a and b; cycles=%v", g.Cycles)
	}
}

func TestGraphCheck_FailsOnCycle(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/a", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "a", Phase: "build", Deps: []string{"b"}})
	mkState(t, root, "docs/changes/b", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "b", Phase: "build", Deps: []string{"a"}})

	_, err := runGraph(t, "--check", "--dir", root)
	if err == nil {
		t.Error("graph --check must return a non-nil error on a cycle")
	}
}

func TestGraphCheck_PassesOnAcyclic(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/a", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "a", Phase: "build", Deps: []string{"b"}})
	mkState(t, root, "docs/changes/b", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "b", Phase: "build"})

	_, err := runGraph(t, "--check", "--dir", root)
	if err != nil {
		t.Errorf("graph --check must exit zero on an acyclic graph, got %v", err)
	}
}
