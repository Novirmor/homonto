package hostruntime

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Novirmor/homonto/internal/agentfm"
)

// ClaudeContextLimit is Claude Code's cap on one additionalContext string
// (10,000 characters); bytes bound characters from above.
const ClaudeContextLimit = 10000

// ClaudeHookInput is the subset of a Claude Code hook's stdin the shim reads.
type ClaudeHookInput struct {
	SessionID    string          `json:"session_id"`
	Event        string          `json:"hook_event_name"`
	Source       string          `json:"source"`
	AgentID      string          `json:"agent_id"`
	AgentType    string          `json:"agent_type"`
	ToolName     string          `json:"tool_name"`
	ToolUseID    string          `json:"tool_use_id"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Cwd          string          `json:"cwd"`
	Reason       string          `json:"reason"`
	Error        string          `json:"error"`
}

// HookDeps are the effects a hook needs, injectable for tests.
type HookDeps struct {
	// Snapshot reads the bound workflow snapshot as JSON.
	Snapshot func(configPath string) (string, error)
	// Relay sends an event to the session's direct server; it fails when no
	// server is listening.
	Relay func(configPath, sessionID string, ev HookEvent) (HookReply, error)
	// AgentDirs are where rendered agents live, searched in order for the
	// running agent's homonto policy.
	AgentDirs []string
}

// agentPolicy finds the running agent's rendered policy. An agent homonto did
// not render has none; a rendered agent whose file cannot be read or parsed
// fails closed.
func agentPolicy(dirs []string, agent string) (agentfm.ClaudePolicy, bool, error) {
	if agent == "" || strings.ContainsAny(agent, `/\`) || agent == "." || agent == ".." {
		return agentfm.ClaudePolicy{}, false, nil
	}
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, agent+".md"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return agentfm.ClaudePolicy{}, false, err
		}
		return agentfm.ClaudePolicyOf(data)
	}
	return agentfm.ClaudePolicy{}, false, nil
}

// coordinatorTool names the MCP tools only the coordinator may call.
func coordinatorTool(tool string) bool { return strings.HasPrefix(tool, "mcp__homonto__") }

// RunClaudeHook handles one Claude Code hook invocation (M4.5): it reads the
// hook input, relays it to the session's runtime core, and writes the hook
// output. It never blocks the harness on an unavailable core; anything that
// needs the core then fails closed in the core's own tools.
func RunClaudeHook(in io.Reader, out io.Writer, configPath string, deps HookDeps) error {
	if !pathOK(configPath) {
		return errors.New("hook: bound absolute config path required")
	}
	var input ClaudeHookInput
	if err := json.NewDecoder(io.LimitReader(in, maxMessage)).Decode(&input); err != nil {
		return errors.New("hook: invalid hook input")
	}
	if input.SessionID == "" || len(input.SessionID) > 256 {
		return errors.New("hook: missing session id")
	}
	ev := HookEvent{Event: input.Event, AgentID: input.AgentID, AgentType: input.AgentType, ToolName: input.ToolName,
		ToolUseID: input.ToolUseID, ToolInput: input.ToolInput, Response: input.ToolResponse, Reason: input.Reason, Error: input.Error}

	// Coordinator-only tool access (ADR 0047): a worker never reaches the
	// workflow or hosting tools, whatever its agent file says.
	if input.Event == "PreToolUse" && input.AgentID != "" && coordinatorTool(input.ToolName) {
		return writeHook(out, input.Event, "", "[wrong_agent] homonto tools are coordinator-only; worker "+jsonText(input.AgentType)+" cannot call "+input.ToolName)
	}
	// The running agent's shell, path and spawn rules (agent-tool-limits,
	// shell-rules): a deny ends the call here; allow and ask stand unless the
	// core denies.
	var policy agentfm.Decision
	if input.Event == "PreToolUse" {
		p, ok, err := agentPolicy(deps.AgentDirs, input.AgentType)
		if err != nil {
			return writeHook(out, input.Event, "", "[policy_unavailable] homonto cannot read this agent's policy; re-run homonto apply")
		}
		if ok {
			var args map[string]any
			_ = json.Unmarshal(input.ToolInput, &args)
			policy = p.Evaluate(input.ToolName, args, input.Cwd)
			if policy.Action == "deny" {
				return writeHook(out, input.Event, "", policy.Reason)
			}
		}
	}
	reply, relayErr := deps.Relay(configPath, input.SessionID, ev)
	if relayErr != nil {
		reply = HookReply{}
	}
	switch input.Event {
	case "SessionStart":
		// Startup, resume, clear and compaction all re-inject the policy and
		// the recovery snapshot (ADR 0064).
		var snap *WorkflowSnapshot
		if raw, err := deps.Snapshot(configPath); err == nil {
			if s, err := ParseSnapshot(raw, configPath); err == nil {
				snap = &s
			}
		}
		return writeHook(out, input.Event, joinContext(ContextBlocksWithin(snap, configPath, reply.Context, ClaudeContextLimit)), "")
	case "PreToolUse":
		if reply.Deny != "" {
			return writeHook(out, input.Event, "", reply.Deny)
		}
		return writeDecision(out, policy.Action)
	default:
		return writeHook(out, input.Event, reply.Context, "")
	}
}

// writeDecision emits an allow or ask decision; anything else leaves the
// harness's own permission rules in charge.
func writeDecision(out io.Writer, action string) error {
	if action != "allow" && action != "ask" {
		return nil
	}
	return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": action,
		"permissionDecisionReason": "homonto policy for this agent"}})
}

func joinContext(blocks []string) string { return strings.Join(blocks, "\n\n") }

// writeHook emits the hook's JSON output, or nothing when there is nothing to say.
func writeHook(out io.Writer, event, context, deny string) error {
	specific := map[string]any{"hookEventName": event}
	switch {
	case deny != "":
		specific["permissionDecision"] = "deny"
		specific["permissionDecisionReason"] = deny
	case context != "":
		specific["additionalContext"] = Bounded(context, ClaudeContextLimit)
	default:
		return nil
	}
	return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": specific})
}

// RelaySocket sends one event to the session's direct server.
func RelaySocket(configPath, sessionID string, ev HookEvent) (HookReply, error) {
	conn, err := net.DialTimeout("unix", SocketPath(configPath, sessionID), time.Second)
	if err != nil {
		return HookReply{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	b, err := json.Marshal(ev)
	if err != nil {
		return HookReply{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return HookReply{}, err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxMessage)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return HookReply{}, err
	}
	var reply HookReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return HookReply{}, err
	}
	return reply, nil
}
