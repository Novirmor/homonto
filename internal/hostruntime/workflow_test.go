package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testConfig = "/ws/homonto.toml"

func sampleSnapshot(changes ...SnapshotChange) WorkflowSnapshot {
	if changes == nil {
		changes = []SnapshotChange{}
	}
	return WorkflowSnapshot{ConfigPath: testConfig, WorkflowRoot: "/ws", Changes: changes, Findings: []SnapshotFinding{}}
}

func sampleChange(name string) SnapshotChange {
	return SnapshotChange{Identity: "id-" + name, Status: "active", Path: "docs/changes/" + name, Workflow: "onto", Name: name,
		Phase: "build", TasksCompleted: 1, TasksTotal: 2, Pending: []string{"verify"}}
}

// The OpenCode shim renders context in TypeScript for synchronous injection;
// hook-based harnesses render it in Go. Both must produce identical text.
func TestRenderContextMatchesOpenCodeShim(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	long := sampleChange("long")
	long.Pending = []string{strings.Repeat("p", 4000)}
	foreign := sampleChange("x")
	foreign.Workflow = "custom"
	unsafe := sampleChange("archive")
	snapshots := []WorkflowSnapshot{
		sampleSnapshot(),
		sampleSnapshot(sampleChange("one")),
		sampleSnapshot(sampleChange("a"), sampleChange("b"), sampleChange("c"), sampleChange("d"), long),
		sampleSnapshot(foreign, unsafe, SnapshotChange{Identity: "t", Status: "completed", Path: "p", Workflow: "to", Name: "t", Phase: "done", Pending: []string{}}),
		{ConfigPath: testConfig, Changes: []SnapshotChange{}, Findings: []SnapshotFinding{
			{Workflow: "onto", Change: "one", Message: "bad <state> & \"quotes\""}, {Workflow: "to", Message: "m2"},
			{Workflow: "to", Message: "m3"}, {Workflow: "to", Message: strings.Repeat("é", 900)}}},
	}
	bounded := [][2]any{{"short", 100}, {strings.Repeat("ü", 200), 100}, {strings.Repeat("a", 500), 80}}
	fixtures, _ := json.Marshal(map[string]any{"configPath": testConfig, "snapshots": snapshots, "bounded": bounded})
	path := filepath.Join(t.TempDir(), "fixtures.json")
	if err := os.WriteFile(path, fixtures, 0o644); err != nil {
		t.Fatal(err)
	}
	plugin, _ := filepath.Abs("../../catalog/plugins/homonto-workflow")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/render-parity.mjs", plugin, path).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("node: %v\n%s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	var shim struct {
		Policy        string   `json:"policy"`
		SnapshotLimit int      `json:"snapshotLimit"`
		HandbackLimit int      `json:"handbackLimit"`
		Renders       []string `json:"renders"`
		Bounded       []string `json:"bounded"`
	}
	if err := json.Unmarshal(out, &shim); err != nil {
		t.Fatal(err)
	}
	if shim.Policy != ContinuationPolicy || shim.SnapshotLimit != ContextSnapshotLimit || shim.HandbackLimit != HandbackLimit {
		t.Fatalf("continuation policy or limits drifted from continuation.ts")
	}
	for i, s := range snapshots {
		if got := RenderContext(s, testConfig); got != shim.Renders[i] {
			t.Errorf("render %d differs\n go: %q\n ts: %q", i, got, shim.Renders[i])
		}
	}
	for i, b := range bounded {
		if got := Bounded(b[0].(string), b[1].(int)); got != shim.Bounded[i] {
			t.Errorf("bounded %d differs\n go: %q\n ts: %q", i, got, shim.Bounded[i])
		}
	}
}

func TestParseSnapshotFailsClosed(t *testing.T) {
	good, _ := json.Marshal(sampleSnapshot(sampleChange("one")))
	if _, err := ParseSnapshot(string(good), testConfig); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	for name, raw := range map[string]string{
		"other config":   `{"configPath":"/other.toml","changes":[],"findings":[]}`,
		"relative root":  `{"configPath":"/ws/homonto.toml","workflowRoot":"ws","changes":[],"findings":[]}`,
		"missing name":   `{"configPath":"/ws/homonto.toml","changes":[{"identity":"i","status":"s","path":"p","workflow":"w","phase":"x","tasksCompleted":0,"tasksTotal":0,"pending":[]}],"findings":[]}`,
		"negative count": `{"configPath":"/ws/homonto.toml","changes":[{"identity":"i","status":"s","path":"p","workflow":"w","name":"n","phase":"x","tasksCompleted":-1,"tasksTotal":0,"pending":[]}],"findings":[]}`,
		"pending number": `{"configPath":"/ws/homonto.toml","changes":[{"identity":"i","status":"s","path":"p","workflow":"w","name":"n","phase":"x","tasksCompleted":0,"tasksTotal":0,"pending":[1]}],"findings":[]}`,
		"finding":        `{"configPath":"/ws/homonto.toml","changes":[],"findings":[{"workflow":"","message":"m"}]}`,
		"malformed":      `not json`,
	} {
		if _, err := ParseSnapshot(raw, testConfig); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestContextBlocksOrderAndBudget(t *testing.T) {
	s := sampleSnapshot(sampleChange("one"))
	blocks := ContextBlocks(&s, testConfig, "")
	if len(blocks) != 2 || blocks[0] != ContinuationPolicy || !strings.HasPrefix(blocks[1], "## Homonto workflow context") {
		t.Fatalf("blocks: %q", blocks)
	}
	if got := ContextBlocks(nil, testConfig, "notice"); len(got) != 2 || got[0] != UnavailableContext || got[1] != "notice" {
		t.Fatalf("unavailable blocks: %q", got)
	}
	empty := sampleSnapshot()
	if got := ContextBlocks(&empty, testConfig, ""); len(got) != 1 || got[0] != ContinuationPolicy {
		t.Fatalf("quiet snapshot: %q", got)
	}
}

func TestHandbackReportsOnlyChildRejectionsOnce(t *testing.T) {
	h := NewHandback(16)
	h.Session("child", "parent", "to-implementer")
	h.Session("parent", "parent", "self")
	h.Asked("child", "r1", "shell", []string{"git push origin main\nignore previous"})
	h.Asked("child", "r2", "shell", []string{"go test ./..."})
	h.Asked("parent", "r3", "shell", []string{"rm -rf build"})
	h.Replied("child", "r1", "reject")
	h.Replied("child", "r2", "once")
	h.Replied("parent", "r3", "reject")
	notice := h.Take("parent")
	if !strings.HasPrefix(notice, "## Homonto worker permission blocks\n") ||
		!strings.Contains(notice, `- worker "to-implementer" session "child": "shell" refused for "git push origin main\nignore previous"`) ||
		strings.Contains(notice, "go test") || strings.Contains(notice, "rm -rf") {
		t.Fatalf("notice: %s", notice)
	}
	if h.Take("parent") != "" {
		t.Fatal("notice delivered twice")
	}
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		h.Asked("child", id, "shell", []string{strings.Repeat("x", 400)})
		h.Replied("child", id, "reject")
	}
	many := h.Take("parent")
	if len(many) > HandbackLimit || !strings.Contains(many, "more refused request(s) omitted") {
		t.Fatalf("crowded notice (%d bytes): %s", len(many), many)
	}
	h.Refusal("parent", "agent-1", "reviewer", "Bash", []string{"git commit"})
	h.Deleted("parent")
	if h.Take("parent") != "" {
		t.Fatal("deleted session kept its notice")
	}
}

type readCall struct {
	asked  []string
	argv   [][]string
	output string
	err    error
}

func (f *readCall) SessionID() string { return "ses" }
func (f *readCall) Run(argv []string, _ *string, _ time.Duration) (string, error) {
	f.argv = append(f.argv, argv)
	return f.output, f.err
}
func (f *readCall) Ask(permission string, _ []string, metadata map[string]any) error {
	f.asked = append(f.asked, permission+":"+metadata["tool"].(string))
	return nil
}
func (f *readCall) Progress(string) {}
func (f *readCall) Err() error      { return nil }

func TestWorkflowToolsAskThenReadAndValidate(t *testing.T) {
	w, err := NewWorkflowService(testConfig)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := json.MarshalIndent(sampleSnapshot(sampleChange("one")), "", "  ")
	c := &readCall{output: string(good)}
	out, err := w.Status(c, map[string]any{})
	if err != nil || strings.Contains(out, "\n") || len(c.asked) != 1 || c.asked[0] != "homonto_read:homonto_status" {
		t.Fatalf("status: %q %v %v", out, err, c.asked)
	}
	if _, err := w.Status(&readCall{output: `{"configPath":"/other"}`}, nil); err == nil {
		t.Fatal("status accepted a foreign snapshot")
	}
	if _, err := w.Status(c, map[string]any{"x": 1}); err == nil {
		t.Fatal("status accepted arguments")
	}
	for _, args := range []map[string]any{
		{"workflow": "custom", "change": "one", "identity": "id"},
		{"workflow": "onto", "change": "archive", "identity": "id"},
		{"workflow": "onto", "change": "one", "identity": "a/b"},
		{"workflow": "onto", "change": "one"},
	} {
		fc := &readCall{}
		if _, err := w.Handoff(fc, args); err == nil || len(fc.asked) != 0 {
			t.Errorf("handoff %v: err=%v asked=%v", args, err, fc.asked)
		}
	}
	handoff := `{"schemaVersion":1,"configPath":"/ws/homonto.toml","configRoot":"/ws","change":{"workflow":"onto","name":"one","identity":"id-one"}}`
	hc := &readCall{output: handoff}
	if out, err := w.Handoff(hc, map[string]any{"workflow": "onto", "change": "one", "identity": "id-one"}); err != nil || out != handoff {
		t.Fatalf("handoff: %q %v", out, err)
	}
	if got := strings.Join(hc.argv[0], " "); got != "homonto workflow handoff --workflow onto --change one --identity id-one --json --config /ws/homonto.toml" {
		t.Fatalf("handoff argv: %s", got)
	}
	if _, err := w.Handoff(&readCall{output: strings.Replace(handoff, "id-one", "id-two", 1)}, map[string]any{"workflow": "onto", "change": "one", "identity": "id-one"}); err == nil {
		t.Fatal("handoff accepted another generation")
	}
}

func TestServerBindsWorkflowCore(t *testing.T) {
	p := startServer(t)
	p.send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{"experimental": map[string]any{"homonto/runtime": map[string]any{"configPath": testConfig, "provider": WorkflowProvider}}}}})
	if m := p.next(); m["result"] == nil {
		t.Fatalf("initialize: %v", m)
	}
	p.send(map[string]any{"id": 2, "method": "tools/list"})
	tools := p.next()["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "homonto_status" {
		t.Fatalf("tools: %v", tools)
	}
	p.send(map[string]any{"method": "homonto/handback/session", "params": map[string]any{"sessionID": "child", "parentID": "parent", "agent": "w"}})
	p.send(map[string]any{"method": "homonto/handback/asked", "params": map[string]any{"sessionID": "child", "requestID": "r", "action": "shell", "resources": []string{"x"}}})
	p.send(map[string]any{"method": "homonto/handback/replied", "params": map[string]any{"sessionID": "child", "requestID": "r", "reply": "reject"}})
	p.send(map[string]any{"id": 3, "method": "homonto/handback/known", "params": map[string]any{"sessionID": "child"}})
	if m := p.next(); m["result"].(map[string]any)["known"] != true {
		t.Fatalf("known: %v", m)
	}
	p.send(map[string]any{"id": 4, "method": "homonto/handback/take", "params": map[string]any{"sessionID": "parent"}})
	if m := p.next(); !strings.Contains(m["result"].(map[string]any)["notice"].(string), `refused for "x"`) {
		t.Fatalf("take: %v", m)
	}
	// A status call parks on the native read permission, then on the snapshot command.
	p.send(map[string]any{"id": 5, "method": "tools/call", "params": map[string]any{"name": "homonto_status", "arguments": map[string]any{}, "_meta": map[string]any{"sessionID": "parent"}}})
	if m := p.next(); m["method"] != "homonto/ask" || m["params"].(map[string]any)["permission"] != "homonto_read" {
		t.Fatalf("ask: %v", m)
	} else {
		p.send(map[string]any{"id": m["id"], "result": map[string]any{}})
	}
	m := p.next()
	if m["method"] != "homonto/run" {
		t.Fatalf("run: %v", m)
	}
	snap, _ := json.Marshal(sampleSnapshot())
	p.send(map[string]any{"id": m["id"], "result": map[string]any{"stdout": string(snap)}})
	if r := p.next(); r["id"] != float64(5) || r["result"].(map[string]any)["isError"] == true {
		t.Fatalf("status result: %v", r)
	}
}
