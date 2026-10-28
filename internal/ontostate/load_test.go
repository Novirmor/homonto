package ontostate

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCurrent_FutureVersion_Rejected(t *testing.T) {
	got, err := parseCurrent([]byte("schema_version: 999\nchange: c\nphase: build\n"), "onto-state.yaml")
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("parseCurrent(999) = %+v, %v; want a schema_version error", got, err)
	}
}

// Only the live schema loads (ADR 0081); older shapes name the fix instead of
// being converted on read.
func TestParseCurrent_OlderShapesRejected(t *testing.T) {
	for name, doc := range map[string]string{
		"unversioned": "change: c\nworkflow: fix\nphase: build\n",
		"schema 1":    "schema_version: 1\nchange: c\nphase: close\n",
		"schema 2":    "schema_version: 2\nchange: c\nphase: build\n",
	} {
		if _, err := parseCurrent([]byte(doc), "onto-state.yaml"); err == nil || !strings.Contains(err.Error(), "no longer supported") {
			t.Errorf("%s: err = %v, want a no-longer-supported error", name, err)
		}
	}
}

func TestParseCurrent_RemovedPresetsAreInvalid(t *testing.T) {
	for _, workflow := range []string{"fix", "tweak"} {
		st, err := parseCurrent([]byte("schema_version: 3\nchange: c\nworkflow: "+workflow+"\nphase: verify\n"), "onto-state.yaml")
		if err != nil {
			t.Fatalf("%s: parse: %v", workflow, err)
		}
		if err := st.Validate(); err == nil || !strings.Contains(err.Error(), "full|light") {
			t.Errorf("%s: Validate = %v, want full|light rejection", workflow, err)
		}
	}
}

func TestParseCurrent_CurrentVersion_RoundTrips(t *testing.T) {
	orig := fullFixtureState()
	b, err := Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := parseCurrent(b, "onto-state.yaml")
	if err != nil {
		t.Fatalf("parseCurrent: %v", err)
	}
	if !reflect.DeepEqual(got, orig) {
		t.Errorf("current-version doc changed on load:\n got  %+v\n want %+v", got, orig)
	}
}
