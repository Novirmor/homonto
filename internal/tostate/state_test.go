package tostate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", FileName)

	want := State{Change: "my-change", Phase: PhasePlan, Created: "2026-07-18", Repos: []string{"api", "web"}}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load = %+v, want %+v", got, want)
	}

	// Atomic write leaves no temp file behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind, stat err = %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		s       State
		wantErr string
	}{
		{"valid plan", State{Change: "x", Phase: PhasePlan}, ""},
		{"valid terminal", State{Change: "x", Phase: PhaseAbandoned, Finished: "2026-07-18"}, ""},
		{"missing change", State{Phase: PhasePlan}, "change is required"},
		{"unknown phase", State{Change: "x", Phase: "build"}, "plan|do|done|abandoned"},
		{"duplicate repos", State{Change: "x", Phase: PhasePlan, Repos: []string{"api", "api"}}, "unique non-empty"},
		{"empty repo", State{Change: "x", Phase: PhasePlan, Repos: []string{""}}, "unique non-empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestTerminal(t *testing.T) {
	for phase, want := range map[string]bool{
		PhasePlan: false, PhaseDo: false, PhaseDone: true, PhaseAbandoned: true,
	} {
		if got := (State{Change: "x", Phase: phase}).Terminal(); got != want {
			t.Errorf("Terminal(%s) = %v, want %v", phase, got, want)
		}
	}
}

func TestLoadRejectsMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()

	if _, err := Load(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("Load(absent) = nil error, want error")
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("{not yaml:::"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("Load(malformed) = nil error, want error")
	}
}
