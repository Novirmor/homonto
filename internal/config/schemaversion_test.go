package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/schema"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_RejectsFutureSchemaVersion(t *testing.T) {
	p := writeConfig(t, "schema_version = 999\n[mcps.demo]\ncommand = [\"true\"]\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("Load of a future schema_version should error")
	}
	if !strings.Contains(err.Error(), "upgrade homonto") {
		t.Errorf("error = %q, want an 'upgrade homonto' message", err)
	}
	if !errors.Is(err, schema.ErrTooNew) {
		t.Fatalf("future schema lost sentinel: %v", err)
	}
}

func TestLoad_AcceptsAbsentAndCurrentSchemaVersion(t *testing.T) {
	// Absent (legacy) loads fine.
	if _, err := Load(writeConfig(t, "[mcps.demo]\ncommand = [\"true\"]\n")); err != nil {
		t.Errorf("absent schema_version should load: %v", err)
	}
	// Explicit current version loads fine.
	for _, version := range []int{0, 1, CurrentConfigSchemaVersion} {
		body := fmt.Sprintf("schema_version = %d\n[mcps.demo]\ncommand = [\"true\"]\n", version)
		if _, err := Load(writeConfig(t, body)); err != nil {
			t.Errorf("schema_version %d should load: %v", version, err)
		}
	}
}

func TestLoadLegacyRejectsLayoutFields(t *testing.T) {
	for _, version := range []int{0, 1} {
		for _, fields := range []string{"[workflow]\ngit='existing'", "[workflow]\ngit=''", "[worktrees]", "[worktrees]\ndir='../trees'"} {
			_, err := Load(writeConfig(t, fmt.Sprintf("schema_version=%d\n%s\n", version, fields)))
			if err == nil || !strings.Contains(err.Error(), "schema_version=2") {
				t.Fatalf("schema %d fields %q: %v", version, fields, err)
			}
		}
	}
}
