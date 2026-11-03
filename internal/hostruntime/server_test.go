package hostruntime

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type peer struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Scanner
}

func startServer(t *testing.T) *peer {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := NewServer(inR, outW, "test")
	done := make(chan error, 1)
	go func() { done <- srv.Serve(); _ = outW.Close() }()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not exit after stdin closed")
		}
	})
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1024*1024), maxMessage)
	return &peer{t, inW, sc}
}

func (p *peer) send(v map[string]any) {
	v["jsonrpc"] = "2.0"
	b, _ := json.Marshal(v)
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

func (p *peer) next() map[string]any {
	p.t.Helper()
	if !p.out.Scan() {
		p.t.Fatalf("server closed: %v", p.out.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(p.out.Bytes(), &m); err != nil {
		p.t.Fatal(err)
	}
	return m
}

// nextNot skips state mirror notifications.
func (p *peer) nextNot(method string) map[string]any {
	for {
		if m := p.next(); m["method"] != method {
			return m
		}
	}
}

func TestServerSpeaksMCPAndParksOnShimCallbacks(t *testing.T) {
	p := startServer(t)
	p.send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{"experimental": map[string]any{"homonto/runtime": map[string]any{"configPath": "/ws/homonto.toml", "provider": "github"}}},
		"_meta":        map[string]any{"now": 1}}})
	if m := p.next(); m["id"] != float64(1) || m["result"].(map[string]any)["protocolVersion"] != ProtocolVersion {
		t.Fatalf("initialize: %v", m)
	}
	p.send(map[string]any{"id": 2, "method": "tools/list"})
	tools := p.next()["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 || tools[0].(map[string]any)["name"] != "homonto_github_draft" {
		t.Fatalf("tools/list: %v", tools)
	}

	// A draft call parks on the read permission, then on the workspace inspect.
	p.send(map[string]any{"id": 3, "method": "tools/call", "params": map[string]any{"name": "homonto_github_draft",
		"arguments": map[string]any{"items": []any{map[string]any{"kind": "issue_comment", "url": "https://github.com/o/r/issues/1",
			"body": "b", "baseOID": "", "headOID": "", "reviewEvent": ""}}},
		"_meta": map[string]any{"now": 2, "sessionID": "s1"}}})
	m := p.nextNot("homonto/state")
	if m["method"] == "homonto/progress" {
		m = p.nextNot("homonto/state")
	}
	if m["method"] != "homonto/ask" || m["params"].(map[string]any)["permission"] != "homonto_github_read" {
		t.Fatalf("expected the read permission request, got %v", m)
	}
	p.send(map[string]any{"id": m["id"], "result": map[string]any{"_meta": map[string]any{"now": 3}}})
	for {
		m = p.nextNot("homonto/state")
		if m["method"] != "homonto/progress" {
			break
		}
	}
	argv := m["params"].(map[string]any)["argv"].([]any)
	if m["method"] != "homonto/run" || argv[0] != "homonto" || argv[2] != "inspect" {
		t.Fatalf("expected the workspace inspect command, got %v", m)
	}
	// Cancelling the tool call while it is parked fails it as cancelled.
	p.send(map[string]any{"method": "notifications/cancelled", "params": map[string]any{"requestId": 3}})
	p.send(map[string]any{"id": m["id"], "error": map[string]any{"code": -32000, "message": "process aborted or disposed"}})
	res := p.nextNot("homonto/state")
	if res["id"] != float64(3) {
		t.Fatalf("expected the tool result, got %v", res)
	}
	r := res["result"].(map[string]any)
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	if r["isError"] != true || !strings.HasPrefix(text, "[invocation_cancelled]") {
		t.Fatalf("cancelled call result: %v", r)
	}
}

func TestServerRejectsUnknownProvider(t *testing.T) {
	p := startServer(t)
	p.send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"capabilities": map[string]any{"experimental": map[string]any{"homonto/runtime": map[string]any{"configPath": "/ws/homonto.toml", "provider": "nope"}}}}})
	if m := p.next(); m["error"] == nil {
		t.Fatalf("unknown provider accepted: %v", m)
	}
}
