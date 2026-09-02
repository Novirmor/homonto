package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadLegacyFixtures pins that state files written by v0.13.0 and v0.14.0
// (schema 1 with the old fingerprint name, and schema 2) load without error
// and keep their recorded entries. Every new schema migration must keep these
// three loading (F2): unknown provenance is reported as unknown, never
// guessed, and never a load failure.
func TestLoadLegacyFixtures(t *testing.T) {
	t.Run("v0.13 main (schema 1)", func(t *testing.T) {
		s := mustLoadFixture(t, "legacy_v013_main.json")
		if len(s.Managed["opencode"]) != 2 {
			t.Fatalf("want 2 opencode entries, got %d", len(s.Managed["opencode"]))
		}
		e, ok := s.Get("opencode", "skill.demo")
		if !ok || e.Desired == "" || e.Applied == "" {
			t.Fatalf("skill.demo entry lost: %+v", e)
		}
	})
	t.Run("v0.14 main (schema 2)", func(t *testing.T) {
		s := mustLoadFixture(t, "legacy_v014_main.json")
		if s.CatalogVersionRecorded() != "0.13.0" {
			t.Fatalf("catalog version lost: %q", s.CatalogVersionRecorded())
		}
		if s.HomontoVersionRecorded() != "0.14.0" {
			t.Fatalf("homonto version lost: %q", s.HomontoVersionRecorded())
		}
		if _, ok := s.Get("opencode", "subagent.onto"); !ok {
			t.Fatal("subagent.onto entry lost")
		}
	})
	t.Run("v0.14 named partition", func(t *testing.T) {
		dir := t.TempDir()
		data := mustReadFixture(t, "legacy_v014_named.json")
		if err := os.WriteFile(filepath.Join(dir, "state.service.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := LoadNamed(dir, "service")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Get("opencode", "skill.service-api"); !ok {
			t.Fatal("partition entry lost")
		}
	})
}

func mustLoadFixture(t *testing.T, name string) *State {
	t.Helper()
	s, err := loadAt(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return s
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
