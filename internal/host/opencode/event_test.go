package opencode

import (
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

const root = "/work/repo"

func event(t *testing.T, body string) protocol.GuardRequest {
	t.Helper()
	req, err := NormalizeEvent(strings.NewReader(body), root)
	if err != nil {
		t.Fatalf("NormalizeEvent: %v\n%s", err, body)
	}
	return req
}

func TestNormalizeWriteEvents(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		paths []string
	}{
		{"write", `{"directory":"/work/repo","input":{"tool":"write","sessionID":"s1"},
			"output":{"args":{"filePath":"/work/repo/src/a.go","content":"x"}}}`,
			[]string{"src/a.go"}},
		{"edit", `{"directory":"/work/repo","input":{"tool":"edit","sessionID":"s1"},
			"output":{"args":{"filePath":"/work/repo/src/b.go"}}}`,
			[]string{"src/b.go"}},
		{"the snake_case spelling", `{"directory":"/work/repo","input":{"tool":"write","sessionID":"s1"},
			"output":{"args":{"file_path":"/work/repo/src/c.go"}}}`,
			[]string{"src/c.go"}},
		{"a relative path", `{"directory":"/work/repo","input":{"tool":"write","sessionID":"s1"},
			"output":{"args":{"path":"src/d.go"}}}`,
			[]string{"src/d.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := event(t, tt.body)
			if req.Host != protocol.HostOpenCode {
				t.Fatalf("request = %+v", req)
			}
			if strings.Join(req.WritePaths, ",") != strings.Join(tt.paths, ",") {
				t.Fatalf("write paths = %v, want %v", req.WritePaths, tt.paths)
			}
			if err := req.Validate(); err != nil {
				t.Fatalf("the normalized request is not valid: %v", err)
			}
		})
	}
}

// TestOneWriteIsOnePath proves the several accepted spellings do not
// become several paths when a release sends more than one of them.
func TestOneWriteIsOnePath(t *testing.T) {
	req := event(t, `{"directory":"/work/repo","input":{"tool":"write","sessionID":"s1"},
		"output":{"args":{"filePath":"/work/repo/src/a.go","file_path":"/work/repo/src/a.go","path":"src/a.go"}}}`)
	if len(req.WritePaths) != 1 {
		t.Fatalf("write paths = %v, want one", req.WritePaths)
	}
}

func TestReadingToolsWriteNothing(t *testing.T) {
	for _, tool := range []string{"read", "grep", "glob", "bash"} {
		req := event(t, `{"directory":"/work/repo","input":{"tool":"`+tool+`","sessionID":"s1"},
			"output":{"args":{"command":"ls"}}}`)
		if len(req.WritePaths) != 0 {
			t.Errorf("%s produced write paths %v", tool, req.WritePaths)
		}
		if err := req.Validate(); err != nil {
			t.Errorf("%s: the normalized request is not valid: %v", tool, err)
		}
	}
}

func TestSessionIDIsStableAndNamespaced(t *testing.T) {
	body := `{"directory":"/work/repo","input":{"tool":"write","sessionID":"abc"},
		"output":{"args":{"filePath":"src/a.go"}}}`
	first := event(t, body)
	if first.SessionID != event(t, body).SessionID {
		t.Fatal("the same host session produced two different session ids")
	}
	if err := identity.ValidateUUID(string(first.SessionID)); err != nil {
		t.Fatalf("the derived session id is not canonical: %v", err)
	}
	// The namespace matters: the same session string in two hosts is two
	// sessions.
	if first.SessionID == identity.SessionIDFor("claude", "abc") {
		t.Fatal("opencode and claude session ids collide")
	}
}

func TestMalformedEventsAreRefused(t *testing.T) {
	for _, body := range []string{
		`{ not json`,
		`{"directory":"/work/repo","input":{},"output":{}}`,
		`{"directory":"/work/repo","input":{"tool":"write"},"output":{"args":{}}}`,
	} {
		if _, err := NormalizeEvent(strings.NewReader(body), root); err == nil {
			t.Errorf("NormalizeEvent(%q) = nil error, want rejection", body)
		}
	}
}

func TestUnknownFieldsAreTolerated(t *testing.T) {
	req := event(t, `{"directory":"/work/repo","input":{"tool":"write","sessionID":"s1","callID":"c1",
		"somethingNew":true},"output":{"args":{"filePath":"src/a.go"},"alsoNew":1}}`)
	if len(req.WritePaths) != 1 {
		t.Fatalf("write paths = %v", req.WritePaths)
	}
}
