package checkpoint

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeSortsMembersAndGates(t *testing.T) {
	sorted := validCheckpoint()
	sorted.UnresolvedGates = []string{"accept-finding", "z-gate"}
	unsorted := validCheckpoint()
	unsorted.Members = []Member{sorted.Members[1], sorted.Members[0]}
	unsorted.UnresolvedGates = []string{"z-gate", "accept-finding"}

	b1, err := Encode(sorted)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Encode(unsorted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("Encode is not order-independent:\n sorted:   %s\n unsorted: %s", b1, b2)
	}
	// Members must sort by ID: the api member id sorts before the docs id.
	if bytes.Index(b1, []byte(string(testAPIID))) > bytes.Index(b1, []byte(string(testDocsID))) {
		t.Error("encoded members are not in ascending ID order")
	}
}

func TestEncodeIsByteStable(t *testing.T) {
	cp := validCheckpoint()
	b1, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Error("Encode of the same value produced different bytes")
	}
	if cp.Members[0].BaseBranch != "main" {
		t.Error("Encode mutated the receiver")
	}
}

func TestEncodeEmptySlicesNotnull(t *testing.T) {
	cp := validCheckpoint()
	cp.Members = nil
	cp.UnresolvedGates = nil
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"members":[]`)) {
		t.Errorf("nil members must encode as [], got: %s", b)
	}
	if !bytes.Contains(b, []byte(`"unresolved_gates":[]`)) {
		t.Errorf("nil gates must encode as [], got: %s", b)
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	cp := validCheckpoint()
	cp.Members[0].IntegrationCommit = strings.Repeat("b", 40)
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cp, got) {
		t.Errorf("round-trip mismatch:\n want: %+v\n got:  %+v", cp, got)
	}
}

func TestDecodeAcceptsUnsortedInput(t *testing.T) {
	cp := validCheckpoint()
	unsorted := validCheckpoint()
	unsorted.Members = []Member{cp.Members[1], cp.Members[0]}
	b, err := Encode(unsorted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(b)); err != nil {
		t.Errorf("Decode rejected unsorted-but-strict input: %v", err)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	cp := validCheckpoint()
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":1,"surprise":true`), 1)
	_, err = Decode(bytes.NewReader(b))
	if !errors.Is(err, ErrUnknownField) {
		t.Errorf("Decode error = %v, want ErrUnknownField", err)
	}
}

func TestDecodeRejectsNestedUnknownField(t *testing.T) {
	cp := validCheckpoint()
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`"base_branch":"main"`), []byte(`"base_branch":"main","leak":"x"`), 1)
	_, err = Decode(bytes.NewReader(b))
	if !errors.Is(err, ErrUnknownField) {
		t.Errorf("Decode error = %v, want ErrUnknownField", err)
	}
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	cp := validCheckpoint()
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	for name, trailing := range map[string]string{
		"second object":  " {}",
		"null":           " null",
		"number":         " 5",
		"garbage":        " zz",
		"truncated pair": " {\"x\":1}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(bytes.NewReader(append(append([]byte{}, b...), []byte(trailing)...)))
			if !errors.Is(err, ErrTrailingData) {
				t.Errorf("Decode error = %v, want ErrTrailingData", err)
			}
		})
	}
}

func TestDecodeRejectsBadSchemaVersion(t *testing.T) {
	cp := validCheckpoint()
	for _, v := range []int{0, 2, -1} {
		cp.SchemaVersion = v
		b, err := Encode(cp)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(bytes.NewReader(b)); !errors.Is(err, ErrUnsupportedSchema) {
			t.Errorf("schema_version %d: error = %v, want ErrUnsupportedSchema", v, err)
		}
	}
}

func TestDecodeRejectsInvalidJSON(t *testing.T) {
	if _, err := Decode(strings.NewReader("not json")); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("error = %v, want ErrInvalidJSON", err)
	}
	if _, err := Decode(strings.NewReader("")); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("empty input: error = %v, want ErrInvalidJSON", err)
	}
}

// TestDecodeIgnoresTrailingWhitespace ensures only values, not whitespace,
// count as trailing data.
func TestDecodeIgnoresTrailingWhitespace(t *testing.T) {
	cp := validCheckpoint()
	b, err := Encode(cp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(append(append([]byte{}, b...), '\n', ' ', '\t'))); err != nil {
		t.Errorf("Decode rejected trailing whitespace: %v", err)
	}
}
