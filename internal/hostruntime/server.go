package hostruntime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ProtocolVersion is the MCP revision the server speaks.
const ProtocolVersion = "2025-06-18"

// maxMessage bounds one JSON-RPC line (a command's stdout is at most 4 MiB in
// the shim runner; JSON escaping can roughly double it).
const maxMessage = 16 * 1024 * 1024

var errCancelled = errors.New("invocation cancelled")

// Providers lists the hosts the runtime can bind.
var Providers = map[string]Provider{"github": GitHub{}, "gitlab": GitLab{}, "bitbucket": Bitbucket{}}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type meta struct {
	Meta struct {
		Now       *int64 `json:"now"`
		SessionID string `json:"sessionID"`
	} `json:"_meta"`
}

type reply struct {
	result json.RawMessage
	err    *rpcError
}

// call is one tools/call invocation running as a coroutine. Only the holder of
// the server's baton runs; the read loop and calls hand it back and forth so
// execution order matches the shim's single-threaded event loop exactly.
type call struct {
	srv       *Server
	key       string
	sessionID string
	cancelled bool
	wake      chan reply
	waiting   int64 // outstanding outgoing request id, 0 when running
}

// Server serves one plugin instance's runtime over newline-delimited JSON-RPC.
type Server struct {
	in      *bufio.Reader
	out     io.Writer
	version string
	svc     *Service
	wf      *WorkflowService
	now     int64
	yield   chan struct{}
	calls   map[string]*call
	pending map[int64]*call
	nextID  int64
	emitted map[string]string
}

// NewServer reads requests from in and writes to out.
func NewServer(in io.Reader, out io.Writer, version string) *Server {
	return &Server{in: bufio.NewReaderSize(in, 64*1024), out: out, version: version, yield: make(chan struct{}),
		calls: map[string]*call{}, pending: map[int64]*call{}, emitted: map[string]string{}}
}

func (s *Server) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.out.Write(append(b, '\n'))
	return err
}

func (s *Server) respond(id json.RawMessage, result any) {
	_ = s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) fail(id json.RawMessage, code int, msg string) {
	_ = s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: msg}})
}

func (s *Server) notify(method string, params any) {
	_ = s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *Server) clock(raw json.RawMessage) {
	var m meta
	if json.Unmarshal(raw, &m) == nil && m.Meta.Now != nil {
		s.now = *m.Meta.Now
	}
}

// emitStates mirrors changed session summaries to the shim, which renders the
// synchronous per-session context from them.
func (s *Server) emitStates() {
	if s.svc == nil {
		return
	}
	var changed []SessionState
	for _, st := range s.svc.States() {
		b, _ := json.Marshal(st)
		if s.emitted[st.SessionID] != string(b) {
			s.emitted[st.SessionID] = string(b)
			changed = append(changed, st)
		}
	}
	if len(changed) > 0 {
		s.notify("homonto/state", map[string]any{"sessions": changed})
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		if buf.Len()+len(chunk) > maxMessage {
			return nil, errors.New("message exceeds limit")
		}
		buf.Write(chunk)
		if !isPrefix {
			return buf.Bytes(), nil
		}
	}
}

// Serve runs until the shim closes stdin.
func (s *Server) Serve() error {
	for {
		line, err := readLine(s.in)
		if errors.Is(err, io.EOF) {
			if s.svc != nil {
				s.svc.Dispose()
			}
			if s.wf != nil {
				s.wf.Handback.Clear()
			}
			return nil
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil || m.JSONRPC != "2.0" {
			s.fail(json.RawMessage("null"), -32700, "parse error")
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			s.clock(m.Params)
			s.request(m)
		case m.Method != "":
			s.clock(m.Params)
			s.notification(m)
			s.emitStates()
		case len(m.ID) > 0:
			s.response(m)
		}
	}
}

func (s *Server) request(m message) {
	switch m.Method {
	case "initialize":
		var p struct {
			Capabilities struct {
				Experimental map[string]struct {
					ConfigPath string `json:"configPath"`
					Provider   string `json:"provider"`
				} `json:"experimental"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.fail(m.ID, -32602, "invalid initialize parameters")
			return
		}
		binding := p.Capabilities.Experimental["homonto/runtime"]
		if binding.Provider == WorkflowProvider {
			wf, err := NewWorkflowService(binding.ConfigPath)
			if err != nil {
				s.fail(m.ID, -32602, err.Error())
				return
			}
			s.wf = wf
			s.respond(m.ID, map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "homonto", "version": s.version}})
			return
		}
		provider, ok := Providers[binding.Provider]
		if !ok {
			s.fail(m.ID, -32602, fmt.Sprintf("unknown provider %q", binding.Provider))
			return
		}
		svc, err := NewService(provider, binding.ConfigPath, func() int64 { return s.now })
		if err != nil {
			s.fail(m.ID, -32602, err.Error())
			return
		}
		svc.cancelSession = s.cancelSession
		s.svc = svc
		s.respond(m.ID, map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "homonto", "version": s.version}})
	case "tools/list":
		if s.wf != nil {
			s.respond(m.ID, map[string]any{"tools": WorkflowToolDefinitions()})
			return
		}
		if s.svc == nil {
			s.fail(m.ID, -32002, "not initialized")
			return
		}
		s.respond(m.ID, map[string]any{"tools": ToolDefinitions(s.svc.provider)})
	case "tools/call":
		s.toolCall(m)
	case "homonto/handback/known", "homonto/handback/take":
		if s.wf == nil {
			s.fail(m.ID, -32002, "not initialized")
			return
		}
		var p struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.SessionID == "" {
			s.fail(m.ID, -32602, "invalid handback request")
			return
		}
		if m.Method == "homonto/handback/known" {
			s.respond(m.ID, map[string]any{"known": s.wf.Handback.HasSession(p.SessionID)})
		} else {
			s.respond(m.ID, map[string]any{"notice": s.wf.Handback.Take(p.SessionID)})
		}
	default:
		s.fail(m.ID, -32601, "method not found")
	}
}

func (s *Server) toolCall(m message) {
	if s.svc == nil && s.wf == nil {
		s.fail(m.ID, -32002, "not initialized")
		return
	}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			SessionID string `json:"sessionID"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || p.Meta.SessionID == "" || len(p.Meta.SessionID) > 256 {
		s.fail(m.ID, -32602, "invalid tool call")
		return
	}
	var args any
	if len(p.Arguments) > 0 {
		v, err := decode(string(p.Arguments))
		if err != nil {
			s.fail(m.ID, -32602, "invalid tool arguments")
			return
		}
		args = v
	}
	var run func(Call, any) (string, error)
	if s.wf != nil {
		run = s.wf.Tool(p.Name)
	} else {
		run = s.svc.Tool(p.Name)
	}
	if run == nil {
		s.fail(m.ID, -32602, "unknown tool")
		return
	}
	c := &call{srv: s, key: string(m.ID), sessionID: p.Meta.SessionID, wake: make(chan reply)}
	s.calls[c.key] = c
	go func() {
		out, err := run(c, args)
		s.emitStates()
		delete(s.calls, c.key)
		if err != nil {
			s.respond(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
		} else {
			s.respond(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": out}}})
		}
		s.yield <- struct{}{}
	}()
	<-s.yield
}

func (s *Server) notification(m message) {
	switch m.Method {
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			if c := s.calls[string(p.RequestID)]; c != nil {
				c.cancelled = true
			}
		}
	case "homonto/before":
		if s.svc == nil {
			return
		}
		var p struct {
			Tool      string          `json:"tool"`
			SessionID string          `json:"sessionID"`
			CallID    string          `json:"callID"`
			Args      json.RawMessage `json:"args"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		args, err := decode(string(p.Args))
		if err != nil {
			return
		}
		s.svc.Before(p.Tool, p.SessionID, p.CallID, args)
	case "homonto/event":
		if s.svc == nil {
			return
		}
		var p struct {
			Event struct {
				Type       string          `json:"type"`
				Properties json.RawMessage `json:"properties"`
				Data       json.RawMessage `json:"data"`
			} `json:"event"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		raw := p.Event.Data
		if len(raw) == 0 || string(raw) == "null" {
			raw = p.Event.Properties
		}
		var props any
		if len(raw) > 0 {
			props, _ = decode(string(raw))
		}
		s.svc.Event(p.Event.Type, props)
	case "homonto/handback/session", "homonto/handback/asked", "homonto/handback/replied", "homonto/handback/deleted":
		if s.wf != nil {
			s.wf.HandbackEvent(strings.TrimPrefix(m.Method, "homonto/handback/"), m.Params)
		}
	case "homonto/dispose":
		if s.svc != nil {
			s.svc.Dispose()
		}
		if s.wf != nil {
			s.wf.Handback.Clear()
		}
	}
}

func (s *Server) response(m message) {
	var id int64
	if json.Unmarshal(m.ID, &id) != nil {
		return
	}
	c := s.pending[id]
	if c == nil {
		return
	}
	delete(s.pending, id)
	if m.Error != nil {
		s.clock(m.Error.Data)
	} else {
		s.clock(m.Result)
	}
	c.wake <- reply{m.Result, m.Error}
	<-s.yield
}

// cancelSession aborts the session's outstanding commands (session.deleted).
func (s *Server) cancelSession(sessionID string) {
	for _, c := range s.calls {
		if c.sessionID == sessionID && c.waiting != 0 {
			s.notify("notifications/cancelled", map[string]any{"requestId": c.waiting, "reason": "session deleted"})
		}
	}
}

// park sends a request to the shim and yields the baton until it answers.
func (c *call) park(method string, params map[string]any) (json.RawMessage, error) {
	s := c.srv
	s.nextID++
	id := s.nextID
	params["call"] = json.RawMessage(c.key)
	s.pending[id] = c
	c.waiting = id
	s.emitStates()
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		delete(s.pending, id)
		c.waiting = 0
		return nil, err
	}
	s.yield <- struct{}{}
	r := <-c.wake
	c.waiting = 0
	if r.err != nil {
		return nil, errors.New(r.err.Message)
	}
	return r.result, nil
}

func (c *call) SessionID() string { return c.sessionID }

func (c *call) Err() error {
	if c.cancelled {
		return errCancelled
	}
	return nil
}

func (c *call) Run(argv []string, stdin *string, timeout time.Duration) (string, error) {
	params := map[string]any{"argv": argv, "timeout": timeout.Milliseconds()}
	if stdin != nil {
		params["stdin"] = *stdin
	}
	raw, err := c.park("homonto/run", params)
	if err != nil {
		return "", err
	}
	var r struct {
		Stdout *string `json:"stdout"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Stdout == nil {
		return "", errors.New("command output exceeded limit")
	}
	return *r.Stdout, nil
}

func (c *call) Ask(permission string, patterns []string, metadata map[string]any) error {
	_, err := c.park("homonto/ask", map[string]any{"permission": permission, "patterns": patterns, "always": []string{}, "metadata": metadata})
	return err
}

func (c *call) Progress(stage string) {
	c.srv.notify("homonto/progress", map[string]any{"call": json.RawMessage(c.key), "stage": stage})
}

// ToolDefinitions are the MCP tool descriptors for one provider. The shim
// verifies its static copy against these at startup to catch version skew.
func ToolDefinitions(p Provider) []map[string]any {
	label := p.Label()
	prefix := "homonto_" + p.ID() + "_"
	idArgs := map[string]any{"draftID": map[string]any{"type": "string"}}
	draftArgs := map[string]any{"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 10, "items": map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"kind", "url", "body", "baseOID", "headOID", "reviewEvent"},
		"properties": map[string]any{
			"kind":        map[string]any{"type": "string", "enum": []string{KindIssueComment, KindPRComment, KindPRReview}},
			"url":         map[string]any{"type": "string"},
			"body":        map[string]any{"type": "string", "maxLength": 8192},
			"baseOID":     map[string]any{"type": "string"},
			"headOID":     map[string]any{"type": "string"},
			"reviewEvent": map[string]any{"type": "string", "enum": []string{"", EventComment, EventRequestChanges}},
		}}}}
	schema := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	return []map[string]any{
		{"name": prefix + "draft", "description": "Stage an immutable, memory-only " + label + " batch. PR bodies gain reviewed OIDs before preview. Formal reviews support COMMENT/REQUEST_CHANGES only. Submit the returned question arguments unchanged to native question; approval is workflow confirmation, not human-only attestation. New batches invalidate earlier session drafts.", "inputSchema": schema(draftArgs, "items")},
		{"name": prefix + "status", "description": "Read the session's draft statuses, exact full preview and native question arguments.", "inputSchema": schema(idArgs, "draftID")},
		{"name": prefix + "publish", "description": "Publish only items approved through correlated native question events, after permission and fresh scope/actor/destination checks. Uncertain outcomes are never blindly retried.", "inputSchema": schema(idArgs, "draftID")},
	}
}
