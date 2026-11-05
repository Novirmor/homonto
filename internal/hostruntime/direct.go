package hostruntime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DirectServer serves the runtime core straight to a harness over MCP stdio
// (M4.1): the harness launches `homonto runtime mcp` itself, so there is no
// plugin shim to execute commands or relay events. The core runs commands in
// the bound config root, takes its session identity from the harness
// environment, and receives hook events over a per-session local socket
// (`homonto runtime hook`).
//
// Permission prompts belong to the harness: it gates every MCP tool call by
// its own native rules before the call reaches the core, so Ask is satisfied
// by the time a tool runs. Publication approval is still the correlated
// native question (ClaudeBefore/ClaudeAnswered).
type DirectServer struct {
	in      *bufio.Reader
	out     io.Writer
	version string

	configPath string
	sessionID  string
	wf         *WorkflowService
	hosts      []*Service

	mu      sync.Mutex // serializes the core: Service and Handback are single-threaded
	writeMu sync.Mutex
	cancels map[string]context.CancelFunc
	cmu     sync.Mutex
	now     func() int64
	exe     string
}

// DirectOptions bind a direct server.
type DirectOptions struct {
	ConfigPath string
	Hosts      []string
	SessionID  string
	Dialect    Dialect
	Version    string
}

// NewDirectServer binds the workflow core and the named hosting providers.
func NewDirectServer(in io.Reader, out io.Writer, o DirectOptions) (*DirectServer, error) {
	wf, err := NewWorkflowService(o.ConfigPath)
	if err != nil {
		return nil, err
	}
	if o.SessionID == "" || len(o.SessionID) > 256 {
		return nil, errors.New("a harness session id is required")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	s := &DirectServer{in: bufio.NewReaderSize(in, 64*1024), out: out, version: o.Version, configPath: o.ConfigPath,
		sessionID: o.SessionID, wf: wf, cancels: map[string]context.CancelFunc{}, exe: exe,
		now: func() int64 { return time.Now().UnixMilli() }}
	seen := map[string]bool{}
	for _, h := range o.Hosts {
		p, ok := Providers[h]
		if !ok || seen[h] {
			return nil, fmt.Errorf("unknown or repeated hosting provider %q", h)
		}
		seen[h] = true
		svc, err := NewService(p, o.ConfigPath, s.now)
		if err != nil {
			return nil, err
		}
		svc.SetDialect(o.Dialect)
		s.hosts = append(s.hosts, svc)
	}
	return s, nil
}

func (s *DirectServer) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

func (s *DirectServer) respond(id json.RawMessage, result any) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *DirectServer) fail(id json.RawMessage, code int, msg string) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: msg}})
}

// Tools lists every tool the direct server serves.
func (s *DirectServer) Tools() []map[string]any {
	tools := WorkflowToolDefinitions()
	for _, h := range s.hosts {
		tools = append(tools, ToolDefinitions(h.provider)...)
	}
	return tools
}

func (s *DirectServer) handler(name string) func(Call, any) (string, error) {
	if run := s.wf.Tool(name); run != nil {
		return run
	}
	for _, h := range s.hosts {
		if run := h.Tool(name); run != nil {
			return run
		}
	}
	return nil
}

// Serve runs until the harness closes stdin. listener, when non-nil, carries
// hook events for this session.
func (s *DirectServer) Serve(listener net.Listener) error {
	if listener != nil {
		go s.acceptHooks(listener)
		defer func() { _ = listener.Close() }()
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		line, err := readLine(s.in)
		if err == io.EOF {
			s.dispose()
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
		case m.Method == "notifications/cancelled":
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				s.cmu.Lock()
				if cancel := s.cancels[string(p.RequestID)]; cancel != nil {
					cancel()
				}
				s.cmu.Unlock()
			}
		case m.Method != "" && len(m.ID) == 0:
			// Other notifications (initialized) need no answer.
		case m.Method == "initialize":
			s.respond(m.ID, map[string]any{"protocolVersion": ProtocolVersion,
				"capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo":   map[string]any{"name": "homonto", "version": s.version}})
		case m.Method == "ping":
			s.respond(m.ID, map[string]any{})
		case m.Method == "tools/list":
			s.respond(m.ID, map[string]any{"tools": s.Tools()})
		case m.Method == "tools/call":
			wg.Add(1)
			go func(m message) { defer wg.Done(); s.toolCall(m) }(m)
		case m.Method != "":
			s.fail(m.ID, -32601, "method not found")
		}
	}
}

func (s *DirectServer) toolCall(m message) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, -32602, "invalid tool call")
		return
	}
	var args any
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		v, err := decode(string(p.Arguments))
		if err != nil {
			s.fail(m.ID, -32602, "invalid tool arguments")
			return
		}
		args = v
	}
	run := s.handler(p.Name)
	if run == nil {
		s.fail(m.ID, -32602, "unknown tool")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cmu.Lock()
	s.cancels[string(m.ID)] = cancel
	s.cmu.Unlock()
	defer func() {
		s.cmu.Lock()
		delete(s.cancels, string(m.ID))
		s.cmu.Unlock()
		cancel()
	}()
	s.mu.Lock()
	out, err := run(&directCall{s: s, ctx: ctx}, args)
	s.mu.Unlock()
	if err != nil {
		s.respond(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
		return
	}
	s.respond(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": out}}})
}

func (s *DirectServer) dispose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hosts {
		h.Dispose()
	}
	s.wf.Handback.Clear()
}

// directCall runs one invocation's commands itself.
type directCall struct {
	s   *DirectServer
	ctx context.Context
}

func (c *directCall) SessionID() string { return c.s.sessionID }

func (c *directCall) Err() error {
	if c.ctx.Err() != nil {
		return errCancelled
	}
	return nil
}

func (c *directCall) Ask(string, []string, map[string]any) error { return c.Err() }

func (c *directCall) Progress(string) {}

// Output limits match the OpenCode shim runner.
const (
	directStdoutLimit = 4 * 1024 * 1024
	directStderrLimit = 64 * 1024
)

type limited struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (l *limited) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.limit {
		l.over = true
		return 0, errors.New("output exceeded limit")
	}
	return l.buf.Write(p)
}

func (c *directCall) Run(argv []string, stdin *string, timeout time.Duration) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty command")
	}
	if err := c.Err(); err != nil {
		return "", errors.New("process aborted or disposed")
	}
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	name := argv[0]
	if name == "homonto" {
		name = c.s.exe
	}
	cmd := exec.CommandContext(ctx, name, argv[1:]...)
	cmd.Dir = filepath.Dir(c.s.configPath)
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	}
	stdout, stderr := &limited{limit: directStdoutLimit}, &limited{limit: directStderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", errors.New("process timed out, aborted or disposed")
	}
	if stdout.over {
		return "", errors.New("process output exceeded limit")
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("process exited %d: %s", exit.ExitCode(), strings.TrimSpace(stderr.buf.String()))
		}
		return "", err
	}
	return stdout.buf.String(), nil
}

// --- hook relay -------------------------------------------------------------

// SocketPath is where a session's direct server listens for hook events. It
// lives in the per-user runtime directory, keyed by config and session, so a
// hook finds it from its own input alone.
func SocketPath(configPath, sessionID string) string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(base) {
		base = os.TempDir()
	}
	sum := sha256.Sum256([]byte(configPath + "\x00" + sessionID))
	leaf := filepath.Join(fmt.Sprintf("homonto-%d", os.Getuid()), hex.EncodeToString(sum[:12])+".sock")
	// A Unix socket path is limited to about 104 bytes on every platform.
	if path := filepath.Join(base, leaf); len(path) < 104 {
		return path
	}
	return filepath.Join("/tmp", leaf)
}

// Listen opens the session's hook socket in a private directory.
func Listen(configPath, sessionID string) (net.Listener, error) {
	path := SocketPath(configPath, sessionID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("hook socket directory %s must be a private directory", dir)
	}
	_ = os.Remove(path)
	return net.Listen("unix", path)
}

// HookEvent is one hook invocation relayed to the session's core.
type HookEvent struct {
	Event     string          `json:"event"`
	AgentID   string          `json:"agentID,omitempty"`
	AgentType string          `json:"agentType,omitempty"`
	ToolName  string          `json:"toolName,omitempty"`
	ToolUseID string          `json:"toolUseID,omitempty"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
	Response  json.RawMessage `json:"toolResponse,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// HookReply is the core's answer: an optional denial reason and an optional
// handback notice to add to the model's context.
type HookReply struct {
	Deny    string `json:"deny,omitempty"`
	Context string `json:"context,omitempty"`
}

func (s *DirectServer) acceptHooks(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			line, err := bufio.NewReaderSize(io.LimitReader(conn, maxMessage), 64*1024).ReadBytes('\n')
			if err != nil && len(line) == 0 {
				return
			}
			var ev HookEvent
			if json.Unmarshal(line, &ev) != nil {
				return
			}
			b, _ := json.Marshal(s.Hook(ev))
			_, _ = conn.Write(append(b, '\n'))
		}()
	}
}

// Hook applies one hook event to the core.
func (s *DirectServer) Hook(ev HookEvent) HookReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	var input, response any
	if len(ev.ToolInput) > 0 {
		input, _ = decode(string(ev.ToolInput))
	}
	if len(ev.Response) > 0 {
		response, _ = decode(string(ev.Response))
	}
	question := ev.ToolName == "AskUserQuestion"
	switch ev.Event {
	case "PreToolUse":
		if question {
			for _, h := range s.hosts {
				if err := h.ClaudeBefore(s.correlationSession(ev), ev.ToolUseID, input); err != nil {
					return HookReply{Deny: err.Error()}
				}
			}
		}
	case "PostToolUse":
		if ev.AgentID != "" {
			s.wf.Handback.Replied(ev.AgentID, requestDigest(ev.ToolName, input), "once")
		}
		if question {
			for _, h := range s.hosts {
				h.ClaudeAnswered(s.correlationSession(ev), ev.ToolUseID, response)
			}
		}
		if ev.AgentID == "" && isDispatch(ev.ToolName) {
			return HookReply{Context: s.wf.Handback.Take(s.sessionID)}
		}
	case "PostToolUseFailure":
		if ev.AgentID != "" {
			reply := "once"
			if refusedError(ev.Error) {
				reply = "reject"
			}
			s.wf.Handback.Replied(ev.AgentID, requestDigest(ev.ToolName, input), reply)
		}
		if question {
			for _, h := range s.hosts {
				h.ClaudeFailed(s.correlationSession(ev), ev.ToolUseID)
			}
		}
		if ev.AgentID == "" && isDispatch(ev.ToolName) {
			return HookReply{Context: s.wf.Handback.Take(s.sessionID)}
		}
	case "PermissionRequest":
		// Claude Code reports a worker's prompt but not the user's answer.
		// The request is settled when the tool runs; one still open when the
		// worker stops was refused (or never answered).
		if ev.AgentID != "" {
			s.wf.Handback.Session(ev.AgentID, s.sessionID, ev.AgentType)
			s.wf.Handback.Asked(ev.AgentID, requestDigest(ev.ToolName, input), ev.ToolName, resourcesOf(input))
		}
	case "SubagentStop":
		if ev.AgentID != "" {
			s.wf.Handback.Abandon(ev.AgentID)
		}
	case "PermissionDenied":
		// A worker's refused tool call. Claude Code reports the refusal
		// itself; the coordinator learns of it when the worker returns.
		if ev.AgentID != "" {
			s.wf.Handback.Refusal(s.sessionID, ev.AgentID, ev.AgentType, ev.ToolName, resourcesOf(input))
		}
	case "SessionStart", "UserPromptSubmit":
		if ev.AgentID == "" {
			return HookReply{Context: s.wf.Handback.Take(s.sessionID)}
		}
	case "SessionEnd":
		for _, h := range s.hosts {
			h.SessionDeleted(s.sessionID)
		}
		s.wf.Handback.Deleted(s.sessionID)
	}
	return HookReply{}
}

// correlationSession scopes publication questions to the coordinator: a
// worker's question never correlates with the coordinator's draft.
func (s *DirectServer) correlationSession(ev HookEvent) string {
	if ev.AgentID != "" {
		return s.sessionID + "\x00" + ev.AgentID
	}
	return s.sessionID
}

func isDispatch(tool string) bool { return tool == "Agent" || tool == "Task" }

// resourcesOf names what a refused tool call touched, for the handback notice.
func resourcesOf(input any) []string {
	m, ok := record(input)
	if !ok {
		return nil
	}
	for _, k := range []string{"command", "file_path", "path", "url", "pattern"} {
		if v, ok := text(m[k]); ok && v != "" {
			return []string{v}
		}
	}
	return nil
}

// requestDigest identifies a tool request by its name and canonical input,
// since Claude Code's PermissionRequest carries no tool_use_id.
func requestDigest(tool string, input any) string {
	sum := sha256.Sum256([]byte(tool + "\x00" + jsonText(input)))
	return hex.EncodeToString(sum[:16])
}

// refusedError recognizes a tool failure that was a permission refusal rather
// than the tool's own error.
func refusedError(msg string) bool {
	msg = strings.ToLower(msg)
	for _, s := range []string{"doesn't want to proceed", "permission", "rejected", "denied"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
