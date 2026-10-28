package ontocli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func mkState(t *testing.T, root, rel string, st ontostate.State) {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = ontostate.CurrentSchemaVersion
	}
	b, err := ontostate.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "onto-state.yaml"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

type graphJSON struct {
	Nodes []struct {
		ID       string `json:"id"`
		Change   string `json:"change"`
		Phase    string `json:"phase"`
		Archived bool   `json:"archived"`
		Kind     string `json:"kind"`
	} `json:"nodes"`
	Edges []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Type string `json:"type"`
	} `json:"edges"`
}

func TestGraphCommand_NodesAndDependsOnEdges(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/alpha", ontostate.State{Change: "alpha", ID: "aaaa1111", Phase: "build", Deps: []string{"beta"}})
	mkState(t, root, "docs/changes/archive/2026-01-01-beta", ontostate.State{Change: "beta", ID: "bbbb2222", Phase: "close", Archived: true})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"graph", "--json", "--dir", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("graph: %v (%s)", err, out.String())
	}

	var g graphJSON
	if err := json.Unmarshal(out.Bytes(), &g); err != nil {
		t.Fatalf("graph --json not valid JSON: %v\n%s", err, out.String())
	}
	byChange := map[string]bool{}
	for _, n := range g.Nodes {
		byChange[n.Change] = n.Archived
	}
	if _, ok := byChange["alpha"]; !ok {
		t.Error("alpha node missing")
	}
	arch, ok := byChange["beta"]
	if !ok || !arch {
		t.Errorf("beta archived node missing/not-archived: %v %v", ok, arch)
	}
	foundEdge := false
	for _, e := range g.Edges {
		if e.From == "alpha" && e.To == "beta" && e.Type == "depends-on" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Errorf("missing depends-on edge alpha->beta; edges=%+v", g.Edges)
	}
}

func TestGraphCommand_ReadOnlyNoConfig(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/solo", ontostate.State{Change: "solo", ID: "cccc3333", Phase: "open"})
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"graph", "--dir", root}) // no homonto.toml present
	if err := cmd.Execute(); err != nil {
		t.Fatalf("graph in config-less workspace should succeed: %v", err)
	}
}

func TestGraphCommand_CapabilityNodesAndImplementsEdges(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/gamma", ontostate.State{Change: "gamma", ID: "dddd4444", Phase: "design"})
	// gamma's delta spec: specs/mycap.md (onto flat delta-spec layout).
	if err := os.WriteFile(filepath.Join(root, "docs", "changes", "gamma", "specs", "mycap.md"), []byte("# mycap"), 0o644); err != nil {
		// ensure the specs dir exists
		if mkErr := os.MkdirAll(filepath.Join(root, "docs", "changes", "gamma", "specs"), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		if wErr := os.WriteFile(filepath.Join(root, "docs", "changes", "gamma", "specs", "mycap.md"), []byte("# mycap"), 0o644); wErr != nil {
			t.Fatal(wErr)
		}
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"graph", "--json", "--dir", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("graph: %v (%s)", err, out.String())
	}
	var g graphJSON
	if err := json.Unmarshal(out.Bytes(), &g); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	// gamma is a change node; mycap is a capability node.
	kindOf := map[string]string{}
	for _, n := range g.Nodes {
		kindOf[n.Change] = n.Kind
	}
	if kindOf["gamma"] != "change" {
		t.Errorf("gamma kind = %q, want change", kindOf["gamma"])
	}
	if kindOf["mycap"] != "capability" {
		t.Errorf("mycap kind = %q, want capability", kindOf["mycap"])
	}
	found := false
	for _, e := range g.Edges {
		if e.From == "gamma" && e.To == "mycap" && e.Type == "implements" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing implements edge gamma->mycap; edges=%+v", g.Edges)
	}
}
