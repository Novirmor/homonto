package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHistoryAutomaticMaintenanceStaysForeground(t *testing.T) {
	l := historyTestLayout(t)
	// Force real maintenance even in this tiny repository, with inherited
	// configuration requesting detachment. Do not disable the work itself.
	config := [][2]string{
		{"maintenance.auto", "true"},
		{"maintenance.autoDetach", "true"},
		{"gc.autoDetach", "true"},
		{"maintenance.gc.enabled", "false"},
		{"maintenance.geometric-repack.enabled", "false"},
		{"maintenance.commit-graph.enabled", "true"},
		{"maintenance.commit-graph.auto", "-1"},
	}
	t.Setenv("GIT_CONFIG_COUNT", fmt.Sprint(len(config)))
	for i, entry := range config {
		t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i), entry[0])
		t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i), entry[1])
	}
	trace := filepath.Join(t.TempDir(), "git-trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	if err := InitManaged(l); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maintenance.autoDetach", "gc.autoDetach"} {
		out, err := historyGit(l, "config", "--bool", "--get", key)
		if err != nil || strings.TrimSpace(string(out)) != "false" {
			t.Errorf("history Git must override %s only for its subprocess: %q, %v", key, out, err)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	maintenance, graph := false, false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != "child_start" {
			continue
		}
		if slices.Contains(event.Argv, "maintenance") {
			maintenance = true
			if slices.Contains(event.Argv, "--detach") {
				t.Errorf("history commit launched detached maintenance: %v", event.Argv)
			}
		}
		graph = graph || slices.Contains(event.Argv, "commit-graph")
	}
	if !maintenance || !graph {
		t.Fatalf("forced maintenance did not run: maintenance=%t commit-graph=%t", maintenance, graph)
	}
	chain := filepath.Join(l.WorkflowRoot, ".git", "objects", "info", "commit-graphs", "commit-graph-chain")
	if data, err := os.ReadFile(chain); err != nil || len(data) == 0 {
		t.Fatalf("maintenance did not finish before history returned: %q, %v", data, err)
	}
	if _, err := historyGit(l, "commit-graph", "verify"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maintenance.autoDetach", "gc.autoDetach"} {
		if got := historyTestGit(t, l.WorkflowRoot, "config", "--bool", "--get", key); got != "true" {
			t.Fatalf("history Git changed caller configuration %s: %q", key, got)
		}
	}
}
