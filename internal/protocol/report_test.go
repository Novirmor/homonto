package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

const explorerPayload = `{
  "facts": ["handler retries three times with fixed delay"],
  "constraints": ["public API must not change"],
  "surfaces": ["internal/retry"],
  "tests": ["internal/retry/retry_test.go"],
  "questions": [
    {"id": "Q-1", "text": "is the budget per-client or global?", "consequence": "wrong scope invalidates the design"}
  ]
}`

const implementerPayload = `{
  "material": {
    "kind": "git_commit",
    "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "content": "f0628fb519ab7e801bfe5dff612a110525e173d073e513f8d33c2233ea5c7ea1"
  },
  "changed_paths": ["internal/retry/budget.go"],
  "assignment_checks": ["go-test"],
  "questions": []
}`

const reviewerPayload = `{
  "acceptance": ["backoff budget configurable"],
  "findings": [
    {
      "id": "F-2",
      "severity": "high",
      "summary": "race on the shared importer state",
      "evidence": ["data race report from -race build"],
      "recommendation": "guard the state with the existing mutex"
    }
  ],
  "questions": []
}`

const skepticPayload = `{
  "assumptions": ["callers tolerate extra latency"],
  "findings": [],
  "questions": []
}`

func validSession() Session {
	return Session{
		HostID:     testSessionID,
		Hostname:   "owl",
		PID:        4242,
		Executable: "/usr/local/bin/claude",
		StartedAt:  time.Date(2026, 8, 24, 9, 30, 0, 0, time.UTC),
	}
}

func validSubmission(report string) ReportSubmission {
	return ReportSubmission{
		ProtocolVersion: CurrentVersion,
		ActionID:        testAction1,
		FreshnessToken:  testToken,
		Role:            RoleExplorer,
		Session:         validSession(),
		Report:          json.RawMessage(report),
	}
}

func submissionJSON(t *testing.T, mutate func(*ReportSubmission)) []byte {
	t.Helper()
	sub := validSubmission(explorerPayload)
	if mutate != nil {
		mutate(&sub)
	}
	b, err := json.Marshal(sub)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeSubmissionRoundTrip(t *testing.T) {
	got, err := DecodeSubmission(bytes.NewReader(submissionJSON(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if got.ActionID != testAction1 || got.Role != RoleExplorer || got.FreshnessToken != testToken {
		t.Errorf("decoded envelope mismatch: %+v", got)
	}
	// encoding/json compacts embedded raw JSON on marshal, so the expected
	// payload is the compacted form of the fixture.
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(explorerPayload)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Report, compact.Bytes()) {
		t.Errorf("report payload not preserved verbatim: %s", got.Report)
	}
	if got.Session.PID != 4242 {
		t.Errorf("session not decoded: %+v", got.Session)
	}
}

func TestDecodeSubmissionRejectsUnknownField(t *testing.T) {
	b := submissionJSON(t, nil)
	b = bytes.Replace(b, []byte(`"protocol_version":1`), []byte(`"protocol_version":1,"mood":"calm"`), 1)
	if _, err := DecodeSubmission(bytes.NewReader(b)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("error = %v, want ErrUnknownField", err)
	}
}

func TestDecodeSubmissionRejectsTrailingJSON(t *testing.T) {
	for name, trailing := range map[string]string{
		"object": " {}",
		"null":   " null",
		"number": " 7",
	} {
		t.Run(name, func(t *testing.T) {
			b := append(append([]byte{}, submissionJSON(t, nil)...), []byte(trailing)...)
			if _, err := DecodeSubmission(bytes.NewReader(b)); !errors.Is(err, ErrTrailingData) {
				t.Errorf("error = %v, want ErrTrailingData", err)
			}
		})
	}
}

func TestDecodeSubmissionRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeSubmission(strings.NewReader("[")); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("error = %v, want ErrInvalidJSON", err)
	}
}

func TestDecodeSubmissionRejectsMalformedEmbeddedReport(t *testing.T) {
	b := []byte(`{"protocol_version":1,"action_id":"` + string(testAction1) +
		`","freshness_token":"` + string(testToken) + `","role":"explorer",` +
		`"session":{"host_id":"` + string(testSessionID) + `","hostname":"owl","pid":1,"executable":"/x","started_at":"2026-08-24T09:30:00Z"},` +
		`"report":{`)
	if _, err := DecodeSubmission(bytes.NewReader(b)); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("error = %v, want ErrInvalidJSON", err)
	}
}

func TestValidateSubmissionAcceptsMatchingAssignment(t *testing.T) {
	action := validAssignment()
	sub := validSubmission(explorerPayload)
	sub.Role = RoleImplementer
	sub.Report = json.RawMessage(implementerPayload)
	if err := ValidateSubmission(action, sub); err != nil {
		t.Errorf("ValidateSubmission: %v", err)
	}
}

func TestValidateSubmissionRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReportSubmission)
	}{
		{"wrong protocol version", func(s *ReportSubmission) { s.ProtocolVersion = 2 }},
		{"action id mismatch", func(s *ReportSubmission) { s.ActionID = testAction3 }},
		{"role mismatch", func(s *ReportSubmission) { s.Role = RoleReviewer }},
		{"unknown role", func(s *ReportSubmission) { s.Role = "oracle" }},
		{"malformed freshness token", func(s *ReportSubmission) { s.FreshnessToken = "short" }},
		{"missing report payload", func(s *ReportSubmission) { s.Report = nil }},
		{"null report payload", func(s *ReportSubmission) { s.Report = json.RawMessage("null") }},
		{"malformed session host id", func(s *ReportSubmission) { s.Session.HostID = "x" }},
		{"empty session hostname", func(s *ReportSubmission) { s.Session.Hostname = "" }},
		{"zero session pid", func(s *ReportSubmission) { s.Session.PID = 0 }},
		{"empty session executable", func(s *ReportSubmission) { s.Session.Executable = "" }},
		{"zero session start time", func(s *ReportSubmission) { s.Session.StartedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := validSubmission(explorerPayload)
			tt.mutate(&sub)
			if err := ValidateSubmission(validAssignment(), sub); err == nil {
				t.Error("ValidateSubmission accepted an invalid submission")
			}
		})
	}
}

func TestValidateSubmissionRejectsDecisionAction(t *testing.T) {
	sub := validSubmission(explorerPayload)
	if err := ValidateSubmission(validDecisionAction(), sub); err == nil {
		t.Error("ValidateSubmission accepted a report against a decision action")
	}
}

func TestDecodeReportSelectsSchemaByRole(t *testing.T) {
	explorerAction := validAssignment()
	explorerAction.Role = RoleExplorer
	explorerAction.ExpectedReport = &ExpectedReport{Kind: RoleExplorer, SchemaVersion: CurrentVersion}

	rep, err := DecodeReport(explorerAction, json.RawMessage(explorerPayload))
	if err != nil {
		t.Fatal(err)
	}
	er, ok := rep.(*ExplorerReport)
	if !ok {
		t.Fatalf("DecodeReport returned %T, want *ExplorerReport", rep)
	}
	if len(er.Facts) != 1 || len(er.Questions) != 1 || er.Questions[0].ID != "Q-1" {
		t.Errorf("explorer payload decoded wrong: %+v", er)
	}

	// The same payload under the implementer schema is an unknown-field
	// violation: role selection is strict, not best-effort.
	implAction := validAssignment()
	if _, err := DecodeReport(implAction, json.RawMessage(explorerPayload)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("error = %v, want ErrUnknownField", err)
	}
}

func TestDecodeReportRejectsWrongKindAndMalformed(t *testing.T) {
	implAction := validAssignment()
	if _, err := DecodeReport(validDecisionAction(), json.RawMessage(explorerPayload)); err == nil {
		t.Error("DecodeReport accepted a report for a decision action")
	}
	if _, err := DecodeReport(implAction, json.RawMessage("{trailing")); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("error = %v, want ErrInvalidJSON", err)
	}
	if _, err := DecodeReport(implAction, json.RawMessage(`{"material":{}} trailing`)); !errors.Is(err, ErrTrailingData) {
		t.Errorf("error = %v, want ErrTrailingData", err)
	}
}

func TestDecodeReportValidatesPayload(t *testing.T) {
	implAction := validAssignment()
	// Missing the required material.content digest.
	bad := `{"material":{"kind":"git_commit","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"changed_paths":[],"assignment_checks":[],"questions":[]}`
	if _, err := DecodeReport(implAction, json.RawMessage(bad)); err == nil {
		t.Error("DecodeReport accepted an invalid implementer payload")
	}
	good := implementerPayload
	if _, err := DecodeReport(implAction, json.RawMessage(good)); err != nil {
		t.Errorf("DecodeReport rejected a valid implementer payload: %v", err)
	}
}

func TestExplorerReportValidate(t *testing.T) {
	valid := func() ExplorerReport {
		var r ExplorerReport
		if err := json.Unmarshal([]byte(explorerPayload), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	tests := []struct {
		name   string
		mutate func(*ExplorerReport)
	}{
		{"blank fact", func(r *ExplorerReport) { r.Facts = []string{" "} }},
		{"question without text", func(r *ExplorerReport) { r.Questions[0].Text = "" }},
		{"question without consequence", func(r *ExplorerReport) { r.Questions[0].Consequence = "" }},
		{"question without id", func(r *ExplorerReport) { r.Questions[0].ID = "" }},
		{
			"duplicate question ids",
			func(r *ExplorerReport) {
				r.Questions = append(r.Questions, Question{ID: "Q-1", Text: "t", Consequence: "c"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.mutate(&r)
			if err := r.Validate(); err == nil {
				t.Error("Validate accepted an invalid explorer report")
			}
		})
	}
	if err := valid().Validate(); err != nil {
		t.Errorf("valid explorer report rejected: %v", err)
	}
}

func TestImplementerReportValidate(t *testing.T) {
	valid := func() ImplementerReport {
		var r ImplementerReport
		if err := json.Unmarshal([]byte(implementerPayload), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	tests := []struct {
		name   string
		mutate func(*ImplementerReport)
	}{
		{
			"git material without commit",
			func(r *ImplementerReport) { r.Material.Commit = "" },
		},
		{
			"git material with patch manifest",
			func(r *ImplementerReport) { r.Material.PatchManifest = []string{"a.patch"} },
		},
		{
			"git material with malformed commit",
			func(r *ImplementerReport) { r.Material.Commit = "xyz" },
		},
		{
			"snapshot material without manifest",
			func(r *ImplementerReport) {
				r.Material = Material{Kind: MaterialSnapshotPatch, Content: fingerprint.Digest(testDigestHex)}
			},
		},
		{
			"snapshot material with commit",
			func(r *ImplementerReport) {
				r.Material = Material{Kind: MaterialSnapshotPatch, Commit: strings.Repeat("a", 40),
					PatchManifest: []string{"a.patch"}, Content: fingerprint.Digest(testDigestHex)}
			},
		},
		{"material without content digest", func(r *ImplementerReport) { r.Material.Content = "" }},
		{"material kind unknown", func(r *ImplementerReport) { r.Material.Kind = "zip" }},
		{
			"changed path escaping",
			func(r *ImplementerReport) { r.ChangedPaths = []string{"../out"} },
		},
		{
			"duplicate changed paths",
			func(r *ImplementerReport) { r.ChangedPaths = []string{"a.go", "a.go"} },
		},
		{"blank check name", func(r *ImplementerReport) { r.AssignmentChecks = []string{" "} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.mutate(&r)
			if err := r.Validate(); err == nil {
				t.Error("Validate accepted an invalid implementer report")
			}
		})
	}
	if err := valid().Validate(); err != nil {
		t.Errorf("valid implementer report rejected: %v", err)
	}
}

func TestReviewerAndSkepticReportValidate(t *testing.T) {
	var rr ReviewerReport
	if err := json.Unmarshal([]byte(reviewerPayload), &rr); err != nil {
		t.Fatal(err)
	}
	if err := rr.Validate(); err != nil {
		t.Fatalf("valid reviewer report rejected: %v", err)
	}
	rr.Findings[0].Severity = "catastrophic"
	if err := rr.Validate(); err == nil {
		t.Error("unknown severity accepted")
	}
	rr.Findings[0].Severity = SeverityHigh
	rr.Findings = append(rr.Findings, Finding{ID: "F-2", Severity: SeverityLow, Summary: "dupe",
		Evidence: []string{"e"}, Recommendation: "r"})
	if err := rr.Validate(); err == nil {
		t.Error("duplicate finding ids accepted")
	}
	rr.Findings = rr.Findings[:1]
	rr.Findings[0].Evidence = nil
	if err := rr.Validate(); err == nil {
		t.Error("finding without evidence accepted")
	}

	var sr SkepticReport
	if err := json.Unmarshal([]byte(skepticPayload), &sr); err != nil {
		t.Fatal(err)
	}
	if err := sr.Validate(); err != nil {
		t.Fatalf("valid skeptic report rejected: %v", err)
	}
	sr.Assumptions = []string{" "}
	if err := sr.Validate(); err == nil {
		t.Error("blank assumption accepted")
	}
}

func TestFindingAndQuestionValidation(t *testing.T) {
	f := Finding{ID: "F-1", Severity: SeverityCritical, Summary: "s", Evidence: []string{"e"}, Recommendation: "r"}
	if err := f.Validate(); err != nil {
		t.Errorf("valid finding rejected: %v", err)
	}
	f.Summary = ""
	if err := f.Validate(); err == nil {
		t.Error("finding without summary accepted")
	}
	f.Summary = "s"
	f.Recommendation = ""
	if err := f.Validate(); err == nil {
		t.Error("finding without recommendation accepted")
	}

	q := Question{ID: "Q-1", Text: "t", Consequence: "c"}
	if err := q.Validate(); err != nil {
		t.Errorf("valid question rejected: %v", err)
	}
	q.Text = ""
	if err := q.Validate(); err == nil {
		t.Error("question without text accepted")
	}
}
