package tostate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveRefusesPlantedParentsWithoutOutsideWrites(t *testing.T) {
	for _, kind := range []string{"symlink", "dangling", "non-directory", "ancestor-symlink", "file-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			original := []byte("change: original\nphase: plan\n")
			outsideFile := filepath.Join(outside, FileName)
			if err := os.WriteFile(outsideFile, original, 0o600); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(root, "records")
			path := filepath.Join(parent, FileName)
			target := outside
			switch kind {
			case "dangling":
				target = filepath.Join(outside, "missing")
			case "non-directory":
				if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "ancestor-symlink":
				path = filepath.Join(parent, "new", "nested", FileName)
			case "file-symlink":
				parent = filepath.Join(root, FileName)
				path, target = parent, outsideFile
			}
			if kind != "non-directory" {
				if err := os.Symlink(target, parent); err != nil {
					t.Fatal(err)
				}
			}
			if err := Save(path, State{Change: "replacement", Phase: PhaseDone}); err == nil {
				t.Fatal("Save accepted planted path")
			}
			data, err := os.ReadFile(outsideFile)
			if err != nil || string(data) != string(original) {
				t.Fatalf("outside state changed: %s, %v", data, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("Save created outside directories/files: %v, %v", entries, err)
			}
		})
	}
}

func TestSaveSupportsExternalRecordsRootAndRelativePaths(t *testing.T) {
	control, records := t.TempDir(), t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(control); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Error(err)
		}
	})
	want := State{Change: "x", Phase: PhaseDo}
	for _, path := range []string{filepath.Join(records, "tasks", "x", FileName), filepath.Join("docs", "tasks", "x", FileName)} {
		if err := Save(path, want); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip: %+v, %v", got, err)
		}
	}
}
