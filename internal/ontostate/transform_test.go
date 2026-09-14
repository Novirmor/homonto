package ontostate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTransformActiveForExplicitReposPreservesFlatStateSemantics(t *testing.T) {
	historical := strings.Repeat("a", 40)
	appBase := strings.Repeat("b", 40)
	webBase := strings.Repeat("c", 40)
	raw := "# retain this record comment\n" +
		"schema_version: 2\n" +
		"change: standardize-ci\n" +
		"id: ci-id\n" +
		"workflow: full\n" +
		"phase: build\n" +
		"base_ref: " + historical + "\n" +
		"base_branch: historical-main\n" +
		"repos: [app, web]\n" +
		"directive: retain the reviewed CI policy\n" +
		"deviates_from: [decision-17]\n" +
		"verify:\n" +
		"  scale: full\n" +
		"  result: pending\n" +
		"observed:\n" +
		"  metrics:\n" +
		"    build: 2026-09-10\n" +
		"  tasks_total: 14\n" +
		"future_metadata:\n" +
		"  retained: true\n"

	result, err := TransformActiveForExplicitRepos([]byte(raw), "ci-id", []ValidatedRepoAnchor{
		{Alias: "web", BaseRef: webBase, BaseBranch: "main", GitCommonDir: "/sources/web/.git"},
		{Alias: "app", BaseRef: appBase, BaseBranch: "main", GitCommonDir: "/sources/app/.git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.ChangedFields, []string{"schema_version", "repo_mode", "repo_bases", "base_ref", "base_branch"}) {
		t.Fatalf("changed fields = %v", result.ChangedFields)
	}
	if !reflect.DeepEqual(result.LegacyConfig, &LegacyConfig{
		BaseRef: historical, BaseBranch: "historical-main", Provenance: LegacyConfigProvenance,
	}) {
		t.Fatalf("legacy config receipt = %+v", result.LegacyConfig)
	}
	if !strings.Contains(string(result.Bytes), "future_metadata:") || !strings.Contains(string(result.Bytes), "retain the reviewed CI policy") {
		t.Fatalf("transformed YAML lost preserved content:\n%s", result.Bytes)
	}

	path := filepath.Join(t.TempDir(), "onto-state.yaml")
	if err := os.WriteFile(path, result.Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != CurrentSchemaVersion || loaded.ID != "ci-id" || loaded.Phase != "build" || loaded.RepoMode != "explicit" {
		t.Fatalf("loaded state = %+v", loaded)
	}
	if loaded.BaseRef != "" || loaded.BaseBranch != "" || loaded.Directive != "retain the reviewed CI policy" || !reflect.DeepEqual(loaded.DeviatesFrom, []string{"decision-17"}) {
		t.Fatalf("operative scalar or workflow fields changed: %+v", loaded)
	}
	for name, mutate := range map[string]func(*State){
		"base ref":    func(state *State) { state.BaseRef = historical },
		"base branch": func(state *State) { state.BaseBranch = "historical-main" },
	} {
		t.Run("rejects mixed explicit "+name, func(t *testing.T) {
			mixed := loaded
			mutate(&mixed)
			if err := mixed.Validate(); err == nil {
				t.Fatal("explicit state accepted a scalar legacy base")
			}
		})
	}
	if !reflect.DeepEqual(loaded.RepoBases, map[string]RepoBase{
		"app": {BaseRef: appBase, BaseBranch: "main", GitCommonDir: "/sources/app/.git"},
		"web": {BaseRef: webBase, BaseBranch: "main", GitCommonDir: "/sources/web/.git"},
	}) {
		t.Fatalf("approved anchors = %+v", loaded.RepoBases)
	}
	inspected, err := InspectRaw(result.Bytes, path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inspected.UnknownFields, []string{"future_metadata"}) {
		t.Fatalf("unknown fields = %v", inspected.UnknownFields)
	}
}

func TestTransformActiveForExplicitReposKeepsCurrentExplicitBytes(t *testing.T) {
	base := strings.Repeat("a", 40)
	raw := "# password change remains open\n" +
		"schema_version: 3\n" +
		"change: issue-52-server-password-length\n" +
		"id: password-id\n" +
		"workflow: full\n" +
		"phase: open\n" +
		"repos: [service-user]\n" +
		"repo_mode: explicit\n" +
		"repo_bases:\n" +
		"  service-user:\n" +
		"    base_ref: " + base + "\n" +
		"    base_branch: main\n" +
		"    git_common_dir: /sources/service-user/.git\n" +
		"future_metadata: retained\n"

	result, err := TransformActiveForExplicitRepos([]byte(raw), "password-id", []ValidatedRepoAnchor{{
		Alias: "service-user", BaseRef: base, BaseBranch: "main", GitCommonDir: "/sources/service-user/.git",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedFields) != 0 || result.LegacyConfig != nil || string(result.Bytes) != raw {
		t.Fatalf("current explicit state changed: fields=%v legacy=%+v\nwant=%q\ngot=%q", result.ChangedFields, result.LegacyConfig, raw, result.Bytes)
	}
}

func TestTransformActiveForExplicitReposRejectsSchemaOneIntegrationRequirement(t *testing.T) {
	base := strings.Repeat("a", 40)
	raw := "schema_version: 1\n" +
		"change: active\n" +
		"id: active-id\n" +
		"phase: build\n" +
		"base_ref: " + base + "\n" +
		"integration_required: true\n" +
		"repos: [app]\n"

	result, err := TransformActiveForExplicitRepos([]byte(raw), "active-id", []ValidatedRepoAnchor{{
		Alias: "app", BaseRef: base, BaseBranch: "main", GitCommonDir: "/sources/app/.git",
	}})
	if err == nil || !strings.Contains(err.Error(), "schema-1 integration_required") {
		t.Fatalf("TransformActiveForExplicitRepos error = %v, want schema-1 integration_required rejection", err)
	}
	if len(result.Bytes) != 0 {
		t.Fatalf("TransformActiveForExplicitRepos prepared a postimage: %q", result.Bytes)
	}
}

func TestTransformActiveForExplicitReposRefusesUnsupportedInputs(t *testing.T) {
	base := strings.Repeat("a", 40)
	anchors := []ValidatedRepoAnchor{{Alias: "app", BaseRef: base, BaseBranch: "main", GitCommonDir: "/sources/app/.git"}}
	valid := "schema_version: 2\nchange: active\nid: active-id\nphase: build\nrepos: [app]\n"
	for _, tc := range []struct {
		name       string
		raw        string
		expectedID string
		anchors    []ValidatedRepoAnchor
		contains   string
	}{
		{name: "mismatched ID", raw: valid, expectedID: "other-id", anchors: anchors, contains: "expected ID"},
		{name: "retired", raw: "schema_version: 1\nchange: active\nid: active-id\nphase: build\nabandoned: true\n", expectedID: "active-id", anchors: anchors, contains: "only active"},
		{name: "legacy decisions", raw: valid + "decisions:\n  directive: old\n", expectedID: "active-id", anchors: anchors, contains: "legacy nested decisions"},
		{name: "legacy metrics", raw: valid + "metrics:\n  tasks_total: 1\n", expectedID: "active-id", anchors: anchors, contains: "legacy nested metrics"},
		{name: "legacy verify mode", raw: valid + "verify:\n  mode: full\n", expectedID: "active-id", anchors: anchors, contains: "legacy verify.mode"},
		{name: "future schema", raw: strings.Replace(valid, "schema_version: 2", "schema_version: 4", 1), expectedID: "active-id", anchors: anchors, contains: "unsupported schema_version"},
		{name: "duplicate YAML field", raw: valid + "phase: open\n", expectedID: "active-id", anchors: anchors, contains: "duplicate field"},
		{name: "multiple documents", raw: valid + "---\nschema_version: 2\nchange: other\nid: other\nphase: open\n", expectedID: "active-id", anchors: anchors, contains: "exactly one YAML document"},
		{name: "alias", raw: "schema_version: 2\nchange: &name active\nid: active-id\nphase: build\nrepos: [app]\nfuture: *name\n", expectedID: "active-id", anchors: anchors, contains: "aliases are not supported"},
		{name: "missing anchor", raw: valid, expectedID: "active-id", anchors: nil, contains: "exactly cover"},
		{name: "mismatched existing anchor", raw: "schema_version: 3\nchange: active\nid: active-id\nphase: build\nrepos: [app]\nrepo_mode: explicit\nrepo_bases:\n  app:\n    base_ref: " + strings.Repeat("b", 40) + "\n    base_branch: main\n    git_common_dir: /sources/app/.git\n", expectedID: "active-id", anchors: anchors, contains: "does not exactly match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := TransformActiveForExplicitRepos([]byte(tc.raw), tc.expectedID, tc.anchors); err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("TransformActiveForExplicitRepos error = %v, want %q", err, tc.contains)
			}
		})
	}
}
