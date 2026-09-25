package permevent

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestParseStreamContract pins the correlated decision stream against the
// upstream OpenCode event shapes at PinnedOpencodeRevision. The fixture is a
// reproduction of the pinned runtime producer's payloads, not the stale v1 SDK
// (asked carries the full request; replied carries sessionID/requestID/reply).
// This offline test does not detect changes in un-fetched upstream revisions.
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

func TestPermissionPluginRuntime(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable; TS runtime contract not run")
	}
	cmd := exec.Command("node", "testdata/plugin-runtime.mjs", "../../catalog/plugins/permission-observer/plugin.ts", "testdata/opencode_events.jsonl")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("TypeScript runtime contract: %v\n%s", err, out)
	}
}

func TestPermissionV2PluginRuntime(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable; V2 runtime contract not run")
	}
	cmd := exec.Command("node", "testdata/v2-runtime.mjs", "../../catalog/plugins/permission-observer/index.ts")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("V2 permission runtime contract: %v\n%s", err, out)
	}
}

func TestV2ProducerContract(t *testing.T) {
	data, err := os.ReadFile("testdata/opencode-v2-producer.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Revision string `json:"revision"`
		Asked    struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"asked"`
		Replied struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"replied"`
		AutomaticAlways struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"automaticAlways"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Revision != "3a103fe0aff726a4edc7492f03f7b88195d9e4c9" || fixture.Asked.Type != EventAsked ||
		fixture.Asked.Data["action"] != "shell" || fixture.Replied.Type != EventReplied || fixture.Replied.Data["reply"] != ReplyOnce {
		t.Fatalf("V2 producer pin or event shape changed: %+v", fixture)
	}
	if _, ok := fixture.Asked.Data["metadata"]; ok {
		t.Fatal("native V2 shell request unexpectedly supplied metadata.command")
	}
	if resources, ok := fixture.Asked.Data["resources"].([]any); !ok || len(resources) != 1 || resources[0] != "git status" {
		t.Fatal("V2 fixture lost the exact single-resource shell request")
	}
	if source, ok := fixture.Asked.Data["source"].(map[string]any); !ok || source["type"] != "tool" || source["id"] != "call_1" {
		t.Fatal("V2 fixture lost tool-call correlation")
	}
	if fixture.AutomaticAlways.Type != EventReplied || fixture.AutomaticAlways.Data["reply"] != ReplyAlways ||
		len(fixture.AutomaticAlways.Data) != len(fixture.Replied.Data) {
		t.Fatal("V2 automatic always reply unexpectedly carries explicit approval provenance")
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
		{"stale SDK ask", []string{`{"type":"permission.updated","properties":{"id":"per_1","sessionID":"s","type":"bash"}}`}},
		{"stale SDK reply", []string{
			`{"type":"permission.asked","properties":{"id":"per_1","sessionID":"s","permission":"bash"}}`,
			`{"type":"permission.replied","properties":{"sessionID":"s","permissionID":"per_1","response":"once"}}`,
		}},
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
	data, err := os.ReadFile("testdata/opencode-producer.json")
	if err != nil {
		t.Fatal(err)
	}
	type contract struct {
		Event            string
		Required, Absent []string
	}
	var producer struct {
		Revision       string
		Asked, Replied contract
	}
	if err := json.Unmarshal(data, &producer); err != nil {
		t.Fatal(err)
	}
	if producer.Revision != PinnedOpencodeRevision || producer.Asked.Event != EventAsked || producer.Replied.Event != EventReplied {
		t.Fatalf("producer pin drift: %+v", producer)
	}
	data, err = os.ReadFile("testdata/opencode_events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event struct {
			Type       string
			Properties map[string]any
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		c := producer.Asked
		if event.Type == producer.Replied.Event {
			c = producer.Replied
		}
		for _, key := range c.Required {
			if _, ok := event.Properties[key]; !ok {
				t.Errorf("%s missing producer field %s", event.Type, key)
			}
		}
		for _, key := range c.Absent {
			if _, ok := event.Properties[key]; ok {
				t.Errorf("%s has stale SDK field %s", event.Type, key)
			}
		}
	}
}
