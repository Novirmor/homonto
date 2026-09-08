package bypasslog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendCreatesAndExtendsVersionedSidecar(t *testing.T) {
	dir := t.TempDir()
	first := Record{At: "2026-09-03T12:00:00Z", Command: "onto bypass recoverable --to build --reason \"recover\"", From: "open", To: "build", Reason: "recover", Skipped: []string{"artifacts"}}
	if err := Append(dir, "recoverable", "onto", first); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := Append(dir, "recoverable", "onto", Record{At: "2026-09-03T12:01:00Z", Command: "onto bypass recoverable --to archive --reason \"stop\"", From: "build", To: "archive", Reason: "stop", Skipped: []string{"merge"}}); err != nil {
		t.Fatalf("second append: %v", err)
	}
	sc, exists, err := Load(filepath.Join(dir, ".onto", "bypass.json"), "recoverable", "onto")
	if err != nil || !exists {
		t.Fatalf("load = (%+v, %t, %v), want sidecar", sc, exists, err)
	}
	if sc.SchemaVersion != schemaVersion || len(sc.Records) != 2 || sc.Records[0].At != first.At || sc.Records[0].Command != first.Command || sc.Records[0].From != first.From || sc.Records[0].To != first.To || sc.Records[0].Reason != first.Reason || len(sc.Records[0].Skipped) != 1 || sc.Records[0].Skipped[0] != "artifacts" {
		t.Fatalf("sidecar = %+v, want versioned records including %+v", sc, first)
	}
}

func TestAppendRejectsEmptyReason(t *testing.T) {
	if err := Append(t.TempDir(), "x", "to", Record{Reason: "  "}); err == nil {
		t.Fatal("Append accepted an empty reason")
	}
}

func TestLoadRejectsMalformedExistingRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".to", "bypass.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"change":"x","framework":"to","records":[{"reason":"missing audit fields"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path, "x", "to"); err == nil {
		t.Fatal("Load accepted a malformed audit record")
	}
}

func TestAppendRefusesSymlinkedSidecarParent(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".to")); err != nil {
		t.Fatal(err)
	}
	record := Record{At: "2026-09-03T12:00:00Z", Command: "to bypass x --to do --reason \"test\"", From: "plan", To: "do", Reason: "test", Skipped: []string{"phase-boundary"}}
	if err := Append(dir, "x", "to", record); err == nil {
		t.Fatal("Append accepted a symlinked sidecar parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "bypass.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was written: %v", err)
	}
}

func TestAppendRefusesPlantedPathsWithoutOutsideWrites(t *testing.T) {
	for _, kind := range []string{"dangling-parent", "non-directory", "ancestor-symlink", "file-symlink", "dangling-file"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			record := Record{At: "2030-01-01T00:00:00Z", Command: "to bypass x", From: "plan", To: "do", Reason: "test", Skipped: []string{"phase-boundary"}}
			if err := Append(outside, "x", "to", record); err != nil {
				t.Fatal(err)
			}
			outsideFile := Path(outside, "to")
			before, err := os.ReadFile(outsideFile)
			if err != nil {
				t.Fatal(err)
			}
			changeDir := root
			planted := filepath.Join(root, ".to")
			target := filepath.Join(outside, "missing")
			switch kind {
			case "non-directory":
				if err := os.WriteFile(planted, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "ancestor-symlink":
				planted = filepath.Join(root, "records")
				target = outside
				changeDir = filepath.Join(planted, "new-change")
			case "file-symlink", "dangling-file":
				if err := os.MkdirAll(planted, 0o755); err != nil {
					t.Fatal(err)
				}
				planted = Path(root, "to")
				if kind == "file-symlink" {
					target = outsideFile
				}
			}
			if kind != "non-directory" {
				if err := os.Symlink(target, planted); err != nil {
					t.Fatal(err)
				}
			}
			if err := Append(changeDir, "x", "to", record); err == nil {
				t.Fatal("Append accepted planted path")
			}
			after, err := os.ReadFile(outsideFile)
			if err != nil || string(before) != string(after) {
				t.Fatalf("outside audit changed: %s, %v", after, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 || entries[0].Name() != ".to" {
				t.Fatalf("outside files created: %v, %v", entries, err)
			}
		})
	}
}
