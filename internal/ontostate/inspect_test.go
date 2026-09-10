package ontostate

import (
	"reflect"
	"strings"
	"testing"
)

func TestInspectRawRetainsLegacyMetadataAndUnknownFields(t *testing.T) {
	state, err := InspectRaw([]byte("schema_version: 2\nchange: migration\nid: change-1\nworkflow: full\nphase: build\nbase_ref: 0123456789012345678901234567890123456789\nbase_branch: main\nrepos: [app]\nfuture_metadata:\n  preserved: true\n"), "onto-state.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != 2 || state.ID != "change-1" || state.Workflow != "full" || state.BaseBranch != "main" {
		t.Fatalf("inspection = %+v", state)
	}
	if !reflect.DeepEqual(state.Repos, []string{"app"}) || !reflect.DeepEqual(state.UnknownFields, []string{"future_metadata"}) {
		t.Fatalf("inspection metadata = %+v", state)
	}
}

func TestInspectRawRejectsAmbiguousYAML(t *testing.T) {
	for _, body := range []string{
		"schema_version: 1\nchange: first\nchange: second\nphase: build\n",
		"schema_version: 1\nchange: &name migration\nphase: build\nfuture: *name\n",
	} {
		if _, err := InspectRaw([]byte(body), "onto-state.yaml"); err == nil || !strings.Contains(err.Error(), "onto-state.yaml") {
			t.Fatalf("InspectRaw(%q) error = %v", body, err)
		}
	}
}

func TestInspectRawRejectsTrailingYAMLDocuments(t *testing.T) {
	valid := "schema_version: 1\nchange: migration\nphase: build\n"
	for _, body := range []string{
		valid + "---\nschema_version: 1\nchange: trailing\nphase: build\n",
		valid + "---\ntrailing: [\n",
	} {
		if _, err := InspectRaw([]byte(body), "onto-state.yaml"); err == nil || !strings.Contains(err.Error(), "onto-state.yaml") {
			t.Fatalf("InspectRaw(%q) error = %v", body, err)
		}
	}
}
