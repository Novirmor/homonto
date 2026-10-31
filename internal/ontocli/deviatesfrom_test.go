package ontocli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/ontostate"
)

func TestSetDeviatesFrom_RoundTripsLeavingOthers(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	a := newChange(t, dir, "alpha")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"set", "deviates-from", "alpha", "--from", "adr-1", "--from", "adr-2", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set deviates-from: %v (%s)", err, out.String())
	}
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.DeviatesFrom) != 2 || st.DeviatesFrom[0] != "adr-1" || st.DeviatesFrom[1] != "adr-2" {
		t.Errorf("deviates-from = %v, want [adr-1 adr-2]", st.DeviatesFrom)
	}
	if st.ID != a.ID {
		t.Errorf("id changed by set deviates-from: %q -> %q", a.ID, st.ID)
	}
}

func TestGraph_DeviatesFromEdge(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/alpha", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "alpha", ID: "aaaa1111", Phase: "build", DeviatesFrom: []string{"adr-7"}})
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
		if e.From == "alpha" && e.To == "adr-7" && e.Type == "deviates-from" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing deviates-from edge alpha->adr-7; edges=%+v", g.Edges)
	}
}
