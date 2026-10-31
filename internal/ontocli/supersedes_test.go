package ontocli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/ontostate"
)

func TestSetSupersedes_RoundTripsLeavingOthers(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	a := newChange(t, dir, "alpha")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"set", "supersedes", "alpha", "--change", "old1", "--change", "old2", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set supersedes: %v (%s)", err, out.String())
	}
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Supersedes) != 2 || st.Supersedes[0] != "old1" || st.Supersedes[1] != "old2" {
		t.Errorf("supersedes = %v, want [old1 old2]", st.Supersedes)
	}
	if st.ID != a.ID {
		t.Errorf("id changed by set supersedes: %q -> %q", a.ID, st.ID)
	}
}

func TestGraph_SupersedesEdge(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/alpha", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "alpha", ID: "aaaa1111", Phase: "build", Supersedes: []string{"legacy"}})
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
		t.Fatal(err)
	}
	found := false
	for _, e := range g.Edges {
		if e.From == "alpha" && e.To == "legacy" && e.Type == "supersedes" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing supersedes edge alpha->legacy; edges=%+v", g.Edges)
	}
}
