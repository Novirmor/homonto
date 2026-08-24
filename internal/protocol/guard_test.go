package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func validGuardRequest() GuardRequest {
	return GuardRequest{
		Host:             HostClaude,
		SessionID:        testSessionID,
		Tool:             "Edit",
		Arguments:        []string{"docs/homonto/tasks/retry-backoff.md"},
		WorkingDirectory: ".",
		WritePaths:       []string{"docs/homonto/tasks/retry-backoff.md"},
	}
}

func TestGuardRequestValidate(t *testing.T) {
	if err := validGuardRequest().Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	opencode := validGuardRequest()
	opencode.Host = HostOpenCode
	opencode.WritePaths = nil
	if err := opencode.Validate(); err != nil {
		t.Errorf("opencode read-only request rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*GuardRequest)
	}{
		{"unknown host", func(r *GuardRequest) { r.Host = "cursor" }},
		{"empty host", func(r *GuardRequest) { r.Host = "" }},
		{"empty tool", func(r *GuardRequest) { r.Tool = "" }},
		{"blank tool", func(r *GuardRequest) { r.Tool = " " }},
		{"malformed session id", func(r *GuardRequest) { r.SessionID = "x" }},
		{"empty working directory", func(r *GuardRequest) { r.WorkingDirectory = "" }},
		{"escaping working directory", func(r *GuardRequest) { r.WorkingDirectory = ".." }},
		{"absolute write path", func(r *GuardRequest) { r.WritePaths = []string{"/etc/passwd"} }},
		{"escaping write path", func(r *GuardRequest) { r.WritePaths = []string{"../out"} }},
		{"duplicate write paths", func(r *GuardRequest) { r.WritePaths = []string{"a", "a"} }},
		{"blank write path", func(r *GuardRequest) { r.WritePaths = []string{" "} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validGuardRequest()
			tt.mutate(&r)
			if err := r.Validate(); err == nil {
				t.Error("Validate accepted an invalid guard request")
			}
		})
	}
}

func TestDecodeGuardRequestStrict(t *testing.T) {
	b, err := json.Marshal(validGuardRequest())
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeGuardRequest(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	want := validGuardRequest()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got:  %+v\n want: %+v", got, want)
	}

	b = append(append([]byte{}, b...), []byte(" null")...)
	if _, err := DecodeGuardRequest(bytes.NewReader(b)); !errors.Is(err, ErrTrailingData) {
		t.Errorf("error = %v, want ErrTrailingData", err)
	}

	// Inject a truly unknown key.
	b3 := bytes.Replace(bytes.TrimSuffix(b, []byte(" null")), []byte(`{"host"`), []byte(`{"host_extra":1,"host"`), 1)
	if _, err := DecodeGuardRequest(bytes.NewReader(b3)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("error = %v, want ErrUnknownField", err)
	}
}

func TestEncodeGuardDecisionDeterministic(t *testing.T) {
	d := GuardDecision{Allow: false, Reason: "write outside the issued scope", Code: "out_of_scope"}
	b1, err := EncodeGuardDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := EncodeGuardDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("EncodeGuardDecision is not deterministic:\n%s\n%s", b1, b2)
	}
	var back GuardDecision
	if err := json.Unmarshal(b1, &back); err != nil {
		t.Fatal(err)
	}
	if back != d {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}
