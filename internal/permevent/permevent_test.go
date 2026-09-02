package permevent

import (
	"os"
	"testing"
)

// TestParseStreamContract pins the correlated decision stream against the
// upstream OpenCode event shapes at PinnedOpencodeRevision. The fixture is a
// capture of the event payloads documented in the pinned revision's
// permission service (asked carries the full request; replied carries only
// sessionID/requestID/reply). If upstream changes a field this test fails and
// the pin must be revisited deliberately.
func TestParseStreamContract(t *testing.T) {
	f, err := os.Open("testdata/opencode_events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ds, err := ParseStream(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 {
		t.Fatalf("want 3 decisions, got %d", len(ds))
	}

	always := ds[0]
	if !always.Approval || !always.Always {
		t.Errorf("per_1 must be an always-approval: %+v", always)
	}
	if always.SessionID != "ses_session1" || always.Permission != "bash" || always.Command != "git status" {
		t.Errorf("per_1 correlation wrong: %+v", always)
	}

	reject := ds[1]
	if reject.Approval || reject.Always {
		t.Errorf("per_2 must be a denial: %+v", reject)
	}
	if reject.Command != "rm -rf /tmp/x" || reject.SessionID != "ses_session2" {
		t.Errorf("per_2 correlation wrong: %+v", reject)
	}

	once := ds[2]
	if !once.Approval || once.Always {
		t.Errorf("per_3 must be a once-approval: %+v", once)
	}
	if once.Command != "go test ./..." {
		t.Errorf("per_3 correlation wrong: %+v", once)
	}
}

func TestCorrelatorFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{"reply without ask", []string{`{"type":"permission.replied","properties":{"sessionID":"s","requestID":"per_x","reply":"once"}}`}},
		{"unknown event", []string{`{"type":"permission.expired","properties":{}}`}},
		{"malformed json", []string{`{"type":`}},
		{"unknown reply", []string{
			`{"type":"permission.asked","properties":{"id":"per_1","sessionID":"s","permission":"bash","patterns":["x"],"metadata":{"command":"x"}}}`,
			`{"type":"permission.replied","properties":{"sessionID":"s","requestID":"per_1","reply":"maybe"}}`,
		}},
		{"session mismatch", []string{
			`{"type":"permission.asked","properties":{"id":"per_1","sessionID":"s1","permission":"bash","patterns":["x"],"metadata":{"command":"x"}}}`,
			`{"type":"permission.replied","properties":{"sessionID":"s2","requestID":"per_1","reply":"once"}}`,
		}},
		{"ask missing session", []string{`{"type":"permission.asked","properties":{"id":"per_1","permission":"bash","patterns":["x"]}}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCorrelator()
			errored := false
			for _, line := range tc.lines {
				if _, err := c.Feed([]byte(line)); err != nil {
					errored = true
				}
			}
			if !errored {
				t.Fatalf("expected at least one error for %s", tc.name)
			}
		})
	}
}

func TestPinnedRevisionRecorded(t *testing.T) {
	if len(PinnedOpencodeRevision) != 40 {
		t.Fatalf("pinned revision must be a full 40-char commit hash, got %q", PinnedOpencodeRevision)
	}
}
