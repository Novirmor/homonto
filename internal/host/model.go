// Package host installs the thin integrations that let Claude Code and
// OpenCode drive a Homonto workflow, and reports what is installed.
//
// # Thin means thin
//
// The generated files contain no workflow transitions, no required-artifact
// rules, no routing policy, and no subagent prompts. Every one of those
// lives in the binary, where it is versioned, tested, and impossible for a
// host to disagree with. A wrapper does four things — probe, ask what is
// next, dispatch it, report the result — and a decision loop for the human.
// If a wrapper ever needs to know a workflow rule, the rule is in the wrong
// place.
//
// # Ownership is explicit and checkable
//
// Every whole file Homonto generates carries a marker naming the digest of
// its own content. A file whose content still matches its marker is
// Homonto's to replace; one that does not is a file a human edited, and
// installation refuses it rather than overwriting the edit. Files Homonto
// SHARES with the user — Claude's settings.json — are projected by managed
// key instead: only the hook entries that invoke Homonto are rewritten, and
// everything else in the document survives byte for byte.
package host

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Tool is a supported host tool.
type Tool string

const (
	// ToolClaude is Claude Code.
	ToolClaude Tool = "claude"
	// ToolOpenCode is OpenCode.
	ToolOpenCode Tool = "opencode"
)

// Known reports whether t is a supported tool.
func (t Tool) Known() bool { return t == ToolClaude || t == ToolOpenCode }

// Dir returns the tool's project-local directory name.
func (t Tool) Dir() (string, error) {
	switch t {
	case ToolClaude:
		return ".claude", nil
	case ToolOpenCode:
		return ".opencode", nil
	}
	return "", fmt.Errorf("host: %q is not a supported host tool", t)
}

// Tools returns every supported tool, in canonical order.
func Tools() []Tool { return []Tool{ToolClaude, ToolOpenCode} }

// Target is one tool's integration in one control repository.
type Target struct {
	Tool Tool `json:"tool"`
	// Dir is the tool's project-local directory, control-root-relative.
	Dir string `json:"dir"`
	// Present reports whether that directory already exists — which is how
	// Detect decides the tool is in use here.
	Present bool `json:"present"`
}

// Action is what installing one file would do.
type Action string

const (
	// ActionCreate: the file does not exist.
	ActionCreate Action = "create"
	// ActionUpdate: the file exists, Homonto owns it, and its content
	// differs.
	ActionUpdate Action = "update"
	// ActionUnchanged: the file exists and already has the right content.
	ActionUnchanged Action = "unchanged"
	// ActionConflict: the file exists and Homonto does not own it. It is
	// never overwritten without an explicit adoption.
	ActionConflict Action = "conflict"
)

// Writes reports whether the action changes the file.
func (a Action) Writes() bool { return a == ActionCreate || a == ActionUpdate }

// PlannedFile is one file the install would write.
type PlannedFile struct {
	// Path is control-root-relative.
	Path string `json:"path"`
	// Mode is the file's permission bits.
	Mode fs.FileMode `json:"mode"`
	// Action is what installing it would do.
	Action Action `json:"action"`
	// Reason explains a conflict, or an unusual action.
	Reason string `json:"reason,omitempty"`

	// content is what would be written. It is unexported because a plan is
	// something to read and approve, not a payload to mutate.
	content []byte
}

// Content returns the bytes the file would be written with.
func (f PlannedFile) Content() []byte { return append([]byte(nil), f.content...) }

// Plan is everything installing one tool's integration would do.
type Plan struct {
	Target Target        `json:"target"`
	Files  []PlannedFile `json:"files"`
	// Ignore lists the .gitignore entries the plan would ensure. Generated
	// host files are project-local and ignored by default; committing them
	// is an opt-in, never a side effect of installing.
	Ignore []string `json:"ignore,omitempty"`
}

// Conflicts returns the files the plan refuses to write.
func (p Plan) Conflicts() []PlannedFile {
	var out []PlannedFile
	for _, f := range p.Files {
		if f.Action == ActionConflict {
			out = append(out, f)
		}
	}
	return out
}

// Changes reports whether applying the plan would write anything.
func (p Plan) Changes() bool {
	for _, f := range p.Files {
		if f.Action.Writes() {
			return true
		}
	}
	return len(p.Ignore) > 0
}

// Observation is what is actually installed for one tool.
type Observation struct {
	Target Target `json:"target"`
	// Installed are the managed files present and unmodified.
	Installed []string `json:"installed,omitempty"`
	// Modified are managed files a human has edited. They are reported
	// rather than repaired: an edit is a statement, and silently reverting
	// it would be an argument.
	Modified []string `json:"modified,omitempty"`
	// Missing are managed files that should exist and do not.
	Missing []string `json:"missing,omitempty"`
	// Foreign are files at managed paths that Homonto never wrote.
	Foreign []string `json:"foreign,omitempty"`
}

// Healthy reports whether the integration is installed and untouched.
func (o Observation) Healthy() bool {
	return len(o.Modified) == 0 && len(o.Missing) == 0 && len(o.Foreign) == 0
}

// InstallRequest asks for one tool's integration.
type InstallRequest struct {
	// Tool is which host to install for.
	Tool Tool
	// Workflow selects which command and skill to install. Only the
	// configured workflow is installed: offering both would let a host
	// start work the workspace is not set up to run.
	Workflow workspacecfg.Workflow
	// Binary is how the wrappers invoke Homonto. Empty means "homonto".
	Binary string
	// Adopt replaces files Homonto does not own. It exists so a user who
	// edited a generated file can deliberately discard the edit; it is
	// never the default.
	Adopt bool
	// Commit opts into committing the generated files rather than
	// ignoring them.
	Commit bool
}

// Validate checks an install request.
func (r InstallRequest) Validate() error {
	if !r.Tool.Known() {
		return fmt.Errorf("host: %q is not a supported host tool", r.Tool)
	}
	switch r.Workflow {
	case workspacecfg.WorkflowTask, workspacecfg.WorkflowChange:
	default:
		return fmt.Errorf("host: workflow %q must be %q or %q",
			r.Workflow, workspacecfg.WorkflowTask, workspacecfg.WorkflowChange)
	}
	if strings.ContainsAny(r.Binary, " \t\n") {
		return fmt.Errorf("host: binary %q must not contain whitespace", r.Binary)
	}
	return nil
}

// binary returns the command the wrappers invoke.
func (r InstallRequest) binary() string {
	if r.Binary == "" {
		return "homonto"
	}
	return r.Binary
}

// entrypoint is the slash command and skill name for a workflow:
// /homonto-task or /homonto-change.
func entrypoint(w workspacecfg.Workflow) string { return "homonto-" + string(w) }
