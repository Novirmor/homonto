package ontostate

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAndMigrate_FutureVersion_Rejected(t *testing.T) {
	got, err := parseAndMigrate([]byte("schema_version: 999\nchange: c\nphase: build\n"), "onto-state.yaml")
	if err == nil {
		t.Fatalf("parseAndMigrate accepted schema_version 999, want error; got %+v", got)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error = %q, want it to name schema_version", err.Error())
	}
}

func TestParseAndMigrate_V1DoesNotAdoptIntegrationRequirement(t *testing.T) {
	got, err := parseAndMigrate([]byte("schema_version: 1\nchange: c\nphase: close\narchived: true\nintegration_required: true\n"), "onto-state.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got.SchemaVersion, CurrentSchemaVersion)
	}
	if got.IntegrationRequired {
		t.Fatal("schema-1 state adopted a field that did not exist in that schema")
	}
}

// legacy 7-field binary onto-state.yaml (no schema_version).
const legacyBinaryYAML = `change: legacy-bin
workflow: fix
phase: build
created: "2026-07-01"
base_ref: main
deps:
  - dep-a
archived: false
`

// legacy rich skill state.yaml (no schema_version), mirroring
// catalog/skills/onto/references/state-yaml.md.
const legacyRichYAML = `change: legacy-rich
workflow: full
phase: verify
created: "2026-07-02"
base_ref: abc123
deps: []
decisions:
  isolation: worktree
  execution: subagent
  tdd: tdd
  directive: "ship it"
verify:
  mode: full
  result: pass
close:
  merged: true
guides: pending
metrics:
  phases:
    open: "2026-07-02"
    build: "2026-07-03"
  tasks_total: 4
  verify_rounds: 2
  upgraded: true
archived: false
`

func TestParseAndMigrate_LegacyBinary_ToV1(t *testing.T) {
	got, err := parseAndMigrate([]byte(legacyBinaryYAML), "onto-state.yaml")
	if err != nil {
		t.Fatalf("parseAndMigrate: %v", err)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, CurrentSchemaVersion)
	}
	if got.Change != "legacy-bin" || got.Workflow != "fix" || got.Phase != "build" {
		t.Errorf("core mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.Deps, []string{"dep-a"}) {
		t.Errorf("Deps = %v, want [dep-a]", got.Deps)
	}
}

func TestParseAndMigrate_LegacyRich_MapsEveryGatedField(t *testing.T) {
	got, err := parseAndMigrate([]byte(legacyRichYAML), "state.yaml")
	if err != nil {
		t.Fatalf("parseAndMigrate: %v", err)
	}
	want := State{
		SchemaVersion: CurrentSchemaVersion,
		Change:        "legacy-rich",
		Workflow:      "full",
		Phase:         "verify",
		Created:       "2026-07-02",
		BaseRef:       "abc123",
		Deps:          []string{},
		Isolation:     "worktree",
		BuildMode:     "subagent",                            // decisions.execution -> build_mode
		TDDMode:       "tdd",                                 // decisions.tdd       -> tdd_mode
		Verify:        Verify{Scale: "full", Result: "pass"}, // verify.mode -> scale
		Close:         Close{Merged: true},
		Directive:     "ship it", // decisions.directive -> directive
		Archived:      false,
		Observed: Observed{
			Metrics:         map[string]string{"open": "2026-07-02", "build": "2026-07-03"},
			TasksTotal:      4,
			VerifyRounds:    2,
			PresetEscalated: true, // metrics.upgraded -> preset_escalated
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rich migration mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestParseAndMigrate_CurrentVersion_IsNoOp(t *testing.T) {
	orig := fullFixtureState()
	b, err := Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	once, err := parseAndMigrate(b, "onto-state.yaml")
	if err != nil {
		t.Fatalf("parseAndMigrate #1: %v", err)
	}
	b2, _ := Marshal(once)
	twice, err := parseAndMigrate(b2, "onto-state.yaml")
	if err != nil {
		t.Fatalf("parseAndMigrate #2: %v", err)
	}
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("migration not idempotent:\n once  %+v\n twice %+v", once, twice)
	}
	if !reflect.DeepEqual(once, orig) {
		t.Errorf("current-version doc changed on migrate:\n got  %+v\n want %+v", once, orig)
	}
}
