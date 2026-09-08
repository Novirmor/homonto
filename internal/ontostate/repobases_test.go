package ontostate

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExplicitRepoBasesValidation(t *testing.T) {
	valid := func() State {
		return State{Change: "cross", Phase: "verify", RepoMode: "explicit", Repos: []string{"a", "b"}, RepoBases: map[string]RepoBase{
			"a": {BaseRef: strings.Repeat("a", 40), BaseBranch: "main", GitCommonDir: "/sources/a/.git"},
			"b": {BaseRef: strings.Repeat("b", 40), BaseBranch: "develop", GitCommonDir: "/sources/b/.git"},
		}}
	}
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*State){
		"missing base":      func(s *State) { delete(s.RepoBases, "b") },
		"wrong alias":       func(s *State) { s.RepoBases["c"] = s.RepoBases["b"]; delete(s.RepoBases, "b") },
		"scalar base":       func(s *State) { s.BaseRef = strings.Repeat("a", 40) },
		"moving base":       func(s *State) { b := s.RepoBases["a"]; b.BaseRef = "HEAD"; s.RepoBases["a"] = b },
		"same object store": func(s *State) { b := s.RepoBases["a"]; s.RepoBases["b"] = b },
		"unknown mode":      func(s *State) { s.RepoMode = "future" },
		"missing mode":      func(s *State) { s.RepoMode = "" },
		"missing heads":     func(s *State) { s.Verify.Result = "pass" },
		"wrong heads": func(s *State) {
			s.Verify.Result = "pass"
			s.Verify.Heads = map[string]string{"a": strings.Repeat("a", 40), "": strings.Repeat("b", 40)}
		},
		"future schema": func(s *State) { s.SchemaVersion = CurrentSchemaVersion + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := valid()
			mutate(&s)
			if s.Validate() == nil {
				t.Fatal("accepted invalid state")
			}
		})
	}
	s := valid()
	s.Verify = Verify{Result: "pass", Heads: map[string]string{"a": strings.Repeat("a", 40), "b": strings.Repeat("b", 40)}}
	path := filepath.Join(t.TempDir(), "onto-state.yaml")
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Validate() != nil || loaded.RepoMode != "explicit" || loaded.RepoBases["b"].BaseBranch != "develop" {
		t.Fatalf("roundtrip: %+v, %v", loaded, err)
	}
}

func TestOldStatesDoNotAcquireExplicitMode(t *testing.T) {
	for _, text := range []string{"change: old\nphase: close\n", "schema_version: 0\nchange: old\nphase: close\n", "schema_version: 1\nchange: old\nphase: close\n", "schema_version: 2\nchange: old\nphase: close\n"} {
		st, err := parseAndMigrate([]byte(text), "test")
		if err != nil || st.RepoMode != "" || len(st.RepoBases) != 0 {
			t.Fatalf("legacy mode changed: %+v %v", st, err)
		}
	}
	if _, err := parseAndMigrate([]byte("schema_version: 2\nchange: old\nphase: open\nrepo_mode: explicit\n"), "test"); err == nil {
		t.Fatal("accepted explicit fields in old schema")
	}
}

func TestLegacyRepoProvenanceValidation(t *testing.T) {
	valid := func() State {
		return State{SchemaVersion: CurrentSchemaVersion, Change: "cross", Phase: "open", RepoMode: "legacy", Repos: []string{"api"}, RepoBases: map[string]RepoBase{
			"":    {GitCommonDir: "/sources/config/.git"},
			"api": {GitCommonDir: "/sources/api/.git", BaseRef: strings.Repeat("a", 40), BaseBranch: "main"},
		}}
	}
	st := valid()
	if err := st.Validate(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "onto-state.yaml")
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, st) {
		t.Fatalf("legacy provenance roundtrip: %+v %v", got, err)
	}
	for name, mutate := range map[string]func(*State){
		"missing config": func(s *State) { delete(s.RepoBases, "") },
		"missing alias":  func(s *State) { delete(s.RepoBases, "api") },
		"extra alias":    func(s *State) { s.RepoBases["extra"] = s.RepoBases["api"] },
		"wrong alias":    func(s *State) { s.RepoBases["other"] = s.RepoBases["api"]; delete(s.RepoBases, "api") },
		"empty identity": func(s *State) { s.RepoBases[""] = RepoBase{} },
		"relative identity": func(s *State) {
			s.RepoBases[""] = RepoBase{GitCommonDir: "config/.git"}
		},
		"noncanonical base": func(s *State) {
			s.RepoBases[""] = RepoBase{GitCommonDir: "/config/.git", BaseRef: "HEAD"}
		},
		"blank branch": func(s *State) {
			s.RepoBases[""] = RepoBase{GitCommonDir: "/config/.git", BaseBranch: " "}
		},
		"conflicting config base": func(s *State) {
			s.RepoBases[""] = RepoBase{GitCommonDir: "/config/.git", BaseRef: strings.Repeat("a", 40)}
			s.BaseRef = strings.Repeat("b", 40)
		},
		"conflicting config target": func(s *State) {
			s.RepoBases[""] = RepoBase{GitCommonDir: "/config/.git", BaseBranch: "main"}
			s.BaseBranch = "develop"
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := valid()
			mutate(&st)
			if err := st.Validate(); err == nil {
				t.Fatal("invalid legacy provenance accepted")
			}
		})
	}
	if _, err := parseAndMigrate([]byte("schema_version: 2\nchange: old\nphase: open\nrepo_mode: legacy\n"), "test"); err == nil {
		t.Fatal("accepted provenance marker in old schema")
	}
}
