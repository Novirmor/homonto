package permevent

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

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
	if fixture.Revision != PinnedOpencodeRevision || fixture.Asked.Type != EventAsked ||
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
