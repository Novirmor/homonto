package protocol

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Host names a host tool the write hook speaks for.
type Host string

const (
	HostClaude   Host = "claude"
	HostOpenCode Host = "opencode"
)

// GuardRequest is what a cooperating host presents to
// `homonto guard --json` before performing an operation: which host and
// session, which tool with which arguments, from which working directory,
// and which paths the operation intends to write. It is a process gate
// for cooperating hosts, not an OS sandbox.
type GuardRequest struct {
	Host             Host               `json:"host"`
	SessionID        identity.SessionID `json:"session_id"`
	Tool             string             `json:"tool"`
	Arguments        []string           `json:"arguments,omitempty"`
	WorkingDirectory string             `json:"working_directory"`
	WritePaths       []string           `json:"write_paths"`
}

// GuardDecision is homonto's answer to a guard request: allow or refuse,
// with a human-readable reason and a machine-readable code the runtime
// owns (this package only requires both to be present).
type GuardDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
	Code   string `json:"code"`
}

// Validate checks the request: known host, well-formed session, non-empty
// tool, and clean working directory and write paths.
func (r GuardRequest) Validate() error {
	switch r.Host {
	case HostClaude, HostOpenCode:
	default:
		return fmt.Errorf("guard: host %q must be %q or %q", r.Host, HostClaude, HostOpenCode)
	}
	if err := identity.ValidateUUID(string(r.SessionID)); err != nil {
		return fmt.Errorf("guard: session_id: %w", err)
	}
	if strings.TrimSpace(r.Tool) == "" {
		return fmt.Errorf("guard: tool must not be blank")
	}
	if err := validateRootRelPath(r.WorkingDirectory, "working_directory"); err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	if err := validateCleanPaths(r.WritePaths, "write_paths"); err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	return nil
}

// Validate checks that a decision always explains itself.
func (d GuardDecision) Validate() error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("guard: decision reason must not be blank")
	}
	if strings.TrimSpace(d.Code) == "" {
		return fmt.Errorf("guard: decision code must not be blank")
	}
	return nil
}
