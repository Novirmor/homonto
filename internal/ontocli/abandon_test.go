package ontocli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/ontostate"
)

func loadChangeState(t *testing.T, dir, name string) ontostate.State {
	t.Helper()
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", name, "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAbandon_MarksChangeLeavingPhase(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "build")
	commitAll(t, dir, "seed")

	if _, err := runOnto(t, "abandon", "feature-x", "--dir", dir); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	st := loadChangeState(t, dir, "feature-x")
	if !st.Abandoned {
		t.Error("Abandoned = false, want true")
	}
	if st.Phase != "build" {
		t.Errorf("Phase = %q, want unchanged build", st.Phase)
	}
}

func TestAdvance_RefusesAbandonedChange(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "design")
	if _, err := runOnto(t, "set", "isolation", "feature-x", "branch", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "abandon", "feature-x", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "seed abandoned")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("advance on abandoned change = nil, want error")
	}
	if !contains(err.Error(), "abandon") {
		t.Errorf("error = %q, want mention of abandon", err.Error())
	}
	if st := loadChangeState(t, dir, "feature-x"); st.Phase != "design" {
		t.Errorf("Phase = %q, want unchanged design", st.Phase)
	}
}

func TestAbandon_RefusesArchivedChange(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "close")
	// mark archived directly
	st := loadChangeState(t, dir, "feature-x")
	st.Archived = true
	if err := ontostate.Save(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "seed archived")

	_, err := runOnto(t, "abandon", "feature-x", "--dir", dir)
	if err == nil {
		t.Fatal("abandon on archived change = nil, want error")
	}
}

func TestGraph_MarksAbandoned(t *testing.T) {
	root := t.TempDir()
	mkState(t, root, "docs/changes/x", ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: "x", ID: "xxxx1111", Phase: "build", Abandoned: true})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"graph", "--json", "--dir", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var g struct {
		Nodes []struct {
			Change    string `json:"change"`
			Abandoned bool   `json:"abandoned"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range g.Nodes {
		if n.Change == "x" {
			found = true
			if !n.Abandoned {
				t.Error("node x abandoned = false, want true")
			}
		}
	}
	if !found {
		t.Error("node x missing")
	}
}
