package tostate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/schema"
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

func TestSourceProvenanceRoundTripAndValidation(t *testing.T) {
	for _, scope := range []string{SourceExplicit, SourceLegacy} {
		t.Run(scope, func(t *testing.T) {
			st := State{SchemaVersion: CurrentSchemaVersion, ID: "stable-id", Change: "scope", Phase: PhaseDo,
				Repos: []string{"api"}, RepoMode: scope, RepoBases: map[string]RepoBase{"api": {GitCommonDir: "/sources/api/.git", BaseRef: strings.Repeat("a", 40), BaseBranch: "main"}}}
			if scope == SourceLegacy {
				st.RepoBases[""] = RepoBase{GitCommonDir: "/config/.git"}
			}
			path := filepath.Join(t.TempDir(), FileName)
			if err := Save(path, st); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil || !reflect.DeepEqual(got, st) {
				t.Fatalf("round trip = %+v, %v; want %+v", got, err, st)
			}
			delete(st.RepoBases, "api")
			if err := Save(path, st); err == nil {
				t.Fatal("saved incomplete source identity")
			}
		})
	}
}

func TestExplicitAnchorsRequiredAndLegacyAnchorsOptional(t *testing.T) {
	for _, base := range []RepoBase{
		{}, {BaseRef: strings.Repeat("a", 40)}, {BaseBranch: "main"},
		{BaseRef: "HEAD", BaseBranch: "main"}, {BaseRef: "abc123", BaseBranch: "main"},
		{BaseRef: strings.Repeat("a", 40), BaseBranch: " "},
	} {
		st := State{SchemaVersion: CurrentSchemaVersion, Change: "anchors", Phase: PhasePlan,
			RepoMode: SourceExplicit, Repos: []string{"api"}, RepoBases: map[string]RepoBase{"api": base}}
		base.GitCommonDir = "/api/.git"
		st.RepoBases["api"] = base
		if err := st.Validate(); err == nil || !strings.Contains(err.Error(), "base_ref") {
			t.Fatalf("accepted incomplete explicit anchors %+v: %v", base, err)
		}
		if err := Save(filepath.Join(t.TempDir(), FileName), st); err == nil {
			t.Fatalf("saved incomplete explicit anchors %+v", base)
		}
	}
	for _, length := range []int{40, 64} {
		st := State{SchemaVersion: CurrentSchemaVersion, Change: "anchors", Phase: PhasePlan,
			RepoMode: SourceExplicit, Repos: []string{"api"}, RepoBases: map[string]RepoBase{
				"api": {GitCommonDir: "/api/.git", BaseRef: strings.Repeat("a", length), BaseBranch: "main"},
			}}
		if err := st.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	st := State{SchemaVersion: CurrentSchemaVersion, Change: "legacy", Phase: PhasePlan,
		RepoMode: SourceLegacy, Repos: []string{"api"}, RepoBases: map[string]RepoBase{
			"": {GitCommonDir: "/control/.git"}, "api": {GitCommonDir: "/api/.git"},
		}}
	if err := st.Validate(); err != nil {
		t.Fatalf("legacy anchors became mandatory: %v", err)
	}
}

func TestLoadSourceSchemaFailsClosed(t *testing.T) {
	for _, body := range []string{
		"schema_version: 999\n",
		"schema_version: -1\n",
		"schema_version: 1\n",
		"repo_mode: explicit\nrepos: [api]\nrepo_bases: {api: {git_common_dir: /api/.git}}\n",
		"schema_version: 1\nrepo_mode: explicit\n",
		"schema_version: 1\nrepo_mode: explicit\nrepos: [api]\nrepo_bases: {api: {git_common_dir: relative}}\n",
		"schema_version: 1\nrepo_mode: legacy\nunknown_authority: dangerous\n",
		"schema_version: 1\nrepo_mode: legacy\n---\nrepo_mode: explicit\n",
	} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			data := []byte("change: scope\nphase: do\n" + body)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("accepted unsafe source schema")
			} else if strings.Contains(body, "999") && !errors.Is(err, schema.ErrTooNew) {
				t.Fatalf("future schema error = %v", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, State{SchemaVersion: 999, Change: "future", Phase: PhaseDo}); !errors.Is(err, schema.ErrTooNew) {
		t.Fatalf("Save future schema = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("future schema created a file: %v", err)
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

func TestSaveDoesNotFollowPredictableTemporaryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Symlink(outside, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, State{Change: "x", Phase: PhasePlan}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("predictable temporary path was followed: %v", err)
	}
}
