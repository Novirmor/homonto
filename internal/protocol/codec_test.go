package protocol

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// goldenReady mirrors testdata/next-ready.golden.json.
func goldenReady() NextResponse {
	return NextResponse{
		ProtocolVersion: CurrentVersion,
		State:           NextReady,
		Actions: []Action{
			{
				ID:             testAction1,
				Kind:           KindAssignment,
				FreshnessToken: testToken,
				Workflow:       workspacecfg.WorkflowTask,
				Path:           "docs/homonto/tasks/retry-backoff.md",
				Phase:          "do",
				Reason:         "implement the retry budget change in services/api",
				Role:           RoleImplementer,
				Prompt:         "Implement the exponential backoff budget described in the plan section of docs/homonto/tasks/retry-backoff.md.",
				Repository: RepositoryRef{
					ID:   testAPIRepoID,
					Path: "services/api",
				},
				WorkingDirectory: ".",
				WriteScope: WriteScope{
					ReadOnly: false,
					Paths:    []string{"internal/retry"},
				},
				ParallelGroupID: "do-retry-budget",
				InputFingerprints: []fingerprint.Digest{
					fingerprint.Digest(testDigestHex),
				},
				ExpectedReport: &ExpectedReport{Kind: RoleImplementer, SchemaVersion: CurrentVersion},
			},
			{
				ID:             testAction2,
				Kind:           KindAssignment,
				FreshnessToken: testToken2,
				Workflow:       workspacecfg.WorkflowTask,
				Path:           "docs/homonto/tasks/retry-backoff.md",
				Phase:          "do",
				Reason:         "document the retry budget change in the docs notes",
				Role:           RoleImplementer,
				Prompt:         "Update docs/notes/retry.md to describe the new backoff budget.",
				Repository: RepositoryRef{
					ID:   identity.RepositoryID("aaaaaaa1-bbbb-4cc2-9ddd-eeeeeeeeeeee"),
					Path: "docs/notes",
				},
				WorkingDirectory: ".",
				WriteScope: WriteScope{
					ReadOnly: false,
					Paths:    []string{"retry.md"},
				},
				ParallelGroupID: "do-retry-budget",
				Dependencies:    []identity.ActionID{testAction3},
				InputFingerprints: []fingerprint.Digest{
					fingerprint.Digest(testDigestHex2),
				},
				ExpectedReport: &ExpectedReport{Kind: RoleImplementer, SchemaVersion: CurrentVersion},
			},
		},
	}
}

// goldenBlocked mirrors testdata/next-blocked-decision.golden.json.
func goldenBlocked() NextResponse {
	a := validDecisionAction()
	a.Prompt = "The skeptic found a race in the parallel importer. Decide whether to accept the finding and order a repair."
	a.Decision.Prompt = "Accept finding F-2 (race on the shared importer state) and open a repair round?"
	return NextResponse{
		ProtocolVersion: CurrentVersion,
		State:           NextBlocked,
		Actions:         []Action{a},
	}
}

// goldenComplete mirrors testdata/next-complete.golden.json.
func goldenComplete() NextResponse {
	return NextResponse{
		ProtocolVersion: CurrentVersion,
		State:           NextComplete,
		Actions:         []Action{},
	}
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// decodeNextResponse strictly decodes next-response bytes: strict parse,
// then envelope validation (exact protocol version included).
func decodeNextResponse(t *testing.T, b []byte) NextResponse {
	t.Helper()
	var resp NextResponse
	if err := decodeStrict(bytes.NewReader(b), &resp); err != nil {
		t.Fatal(err)
	}
	if err := resp.Validate(); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGoldenEncodeMatchesBytes(t *testing.T) {
	for _, tt := range []struct {
		name string
		resp NextResponse
	}{
		{"next-ready.golden.json", goldenReady()},
		{"next-blocked-decision.golden.json", goldenBlocked()},
		{"next-complete.golden.json", goldenComplete()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EncodeNextResponse(tt.resp)
			if err != nil {
				t.Fatal(err)
			}
			want := readGolden(t, tt.name)
			if !bytes.Equal(got, want) {
				t.Errorf("EncodeNextResponse mismatch with %s:\n--- got ---\n%s\n--- want ---\n%s", tt.name, got, want)
			}
		})
	}
}

func TestGoldenDecodeRoundTrips(t *testing.T) {
	for _, tt := range []struct {
		name string
		resp NextResponse
	}{
		{"next-ready.golden.json", goldenReady()},
		{"next-blocked-decision.golden.json", goldenBlocked()},
		{"next-complete.golden.json", goldenComplete()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeNextResponse(t, readGolden(t, tt.name))
			if !reflect.DeepEqual(got, tt.resp) {
				t.Errorf("decoded golden does not match the fixture:\n got:  %+v\n want: %+v", got, tt.resp)
			}
		})
	}
}

// TestGoldenReencodeIsIdempotent proves encode(decode(golden)) reproduces
// the golden bytes exactly.
func TestGoldenReencodeIsIdempotent(t *testing.T) {
	for _, name := range []string{
		"next-ready.golden.json",
		"next-blocked-decision.golden.json",
		"next-complete.golden.json",
	} {
		t.Run(name, func(t *testing.T) {
			decoded := decodeNextResponse(t, readGolden(t, name))
			reencoded, err := EncodeNextResponse(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reencoded, readGolden(t, name)) {
				t.Errorf("re-encoding %s is not idempotent:\n%s", name, reencoded)
			}
		})
	}
}

// TestCompleteResponseCarriesEmptyActionsArray pins the complete state to an
// explicitly present empty actions array, never an omitted key.
func TestCompleteResponseCarriesEmptyActionsArray(t *testing.T) {
	golden := readGolden(t, "next-complete.golden.json")
	if !bytes.Contains(golden, []byte("\"actions\": []")) {
		t.Errorf("complete golden must contain \"actions\": [], got:\n%s", golden)
	}
	resp := decodeNextResponse(t, golden)
	if resp.Actions == nil || len(resp.Actions) != 0 {
		t.Errorf("complete response decoded nil or non-empty actions: %+v", resp.Actions)
	}
}

func TestEncodeNextResponseNormalizesNilActions(t *testing.T) {
	resp := goldenComplete()
	resp.Actions = nil
	b, err := EncodeNextResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("\"actions\": []")) {
		t.Errorf("nil actions must encode as [], got:\n%s", b)
	}
}

func TestDecodeNextResponseRejectsBadVersionAndTrailing(t *testing.T) {
	golden := readGolden(t, "next-complete.golden.json")
	bumped := bytes.Replace(golden, []byte(`"protocol_version": 1`), []byte(`"protocol_version": 2`), 1)
	var bumpedResponse NextResponse
	if err := decodeStrict(bytes.NewReader(bumped), &bumpedResponse); err == nil && bumpedResponse.Validate() == nil {
		t.Error("future protocol version accepted")
	}
	trailing := append(append([]byte{}, golden...), []byte(" {}")...)
	var trailingResponse NextResponse
	if err := decodeStrict(bytes.NewReader(trailing), &trailingResponse); err == nil {
		t.Error("trailing JSON accepted")
	}
}
