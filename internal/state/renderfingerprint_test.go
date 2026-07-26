package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A state file written by the previous schema loads without error and comes
// back with an EMPTY render fingerprint, which already means "force
// re-render". No value-preserving migration is written on purpose: the field
// is a cache, and the catalog version bump that ships this change forces a
// re-materialize for every user anyway, so copying the old value would buy
// nothing while adding a code path that could itself be wrong.
func TestLoad_SchemaV1StateForcesOneRerender(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "schemaVersion": 1,
  "managed": {},
  "catalogVersion": "0.9.0",
  "subagentRenderFingerprint": "deadbeef"
}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("a schema-1 state file must still load: %v", err)
	}
	if got := s.RenderFingerprintRecorded(); got != "" {
		t.Errorf("RenderFingerprintRecorded() = %q, want empty so the next apply re-renders", got)
	}
	if s.CatalogVersionRecorded() != "0.9.0" {
		t.Errorf("unrelated fields must survive the schema bump, got %q", s.CatalogVersionRecorded())
	}
}

// Saving stamps the current schema and writes the new key, not the old one.
func TestSave_WritesRenamedFingerprintKey(t *testing.T) {
	dir := t.TempDir()
	s := newState()
	s.SetRenderFingerprint("abc123")
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"renderFingerprint\"") {
		t.Errorf("saved state must use the renamed key:\n%s", data)
	}
	if strings.Contains(string(data), "subagentRenderFingerprint") {
		t.Errorf("saved state must not write the old key:\n%s", data)
	}
	var probe struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.SchemaVersion != CurrentStateSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", probe.SchemaVersion, CurrentStateSchemaVersion)
	}
}

// Forward-safety is unchanged: a state file from a newer schema is refused.
func TestLoad_RejectsFutureStateSchema(t *testing.T) {
	dir := t.TempDir()
	future := `{"schemaVersion": 99, "managed": {}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a future state schema version must be refused")
	}
}
