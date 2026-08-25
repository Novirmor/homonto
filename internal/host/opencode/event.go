// Package opencode normalizes OpenCode's plugin events into Homonto's
// protocol.
//
// Everything here is translation, for the same reason as the Claude
// package: no decision is made here, so a second host tool costs a file
// rather than a fork.
package opencode

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// Event names the plugin hooks Homonto installs.
const (
	// EventSessionCreated fires when a session begins; the resume probe
	// runs there.
	EventSessionCreated = "session.created"
	// EventToolExecuteBefore fires before a tool runs; the write guard
	// runs there.
	EventToolExecuteBefore = "tool.execute.before"
)

// Event is what the plugin sends Homonto: the project directory plus the
// hook's own input and output objects, forwarded verbatim. Only the fields
// Homonto reads are declared, so an OpenCode release that adds a field
// does not become a Homonto outage.
type Event struct {
	Directory string `json:"directory"`
	Input     struct {
		Tool      string `json:"tool"`
		SessionID string `json:"sessionID"`
		CallID    string `json:"callID"`
	} `json:"input"`
	Output struct {
		Args map[string]any `json:"args"`
	} `json:"output"`
}

// Decode reads a plugin event.
func Decode(r io.Reader) (Event, error) {
	var e Event
	if err := json.NewDecoder(r).Decode(&e); err != nil {
		return Event{}, fmt.Errorf("opencode: decode plugin event: %w", err)
	}
	if strings.TrimSpace(e.Input.Tool) == "" {
		return Event{}, fmt.Errorf("opencode: plugin event names no tool")
	}
	return e, nil
}

// writeTools maps each writing tool to the argument fields that carry a
// path. A tool absent from this table writes no file — and one that starts
// writing without being added here is caught by the final diff gate, which
// is the half of the boundary that does not depend on the host.
var writeTools = map[string][]string{
	"write": {"filePath", "file_path", "path"},
	"edit":  {"filePath", "file_path", "path"},
	"patch": {"filePath", "file_path", "path"},
}

// NormalizeEvent turns a tool.execute.before event into a guard request.
func NormalizeEvent(r io.Reader, root string) (protocol.GuardRequest, error) {
	e, err := Decode(r)
	if err != nil {
		return protocol.GuardRequest{}, err
	}
	dir := root
	if e.Directory != "" {
		dir = e.Directory
	}
	paths, err := writePaths(e, dir)
	if err != nil {
		return protocol.GuardRequest{}, err
	}
	return protocol.GuardRequest{
		Host:             protocol.HostOpenCode,
		SessionID:        identity.SessionIDFor("opencode", e.Input.SessionID),
		Tool:             e.Input.Tool,
		WorkingDirectory: relativeTo(root, dir),
		WritePaths:       paths,
	}, nil
}

// writePaths extracts the workspace-relative paths a tool call intends to
// write. OpenCode has spelled the path argument differently across
// releases, so several names are accepted — and the FIRST one present
// wins, rather than all of them, so one write is one path.
func writePaths(e Event, dir string) ([]string, error) {
	fields, writes := writeTools[strings.ToLower(e.Input.Tool)]
	if !writes {
		return nil, nil
	}
	seen := map[string]bool{}
	var paths []string
	for _, field := range fields {
		value, _ := e.Output.Args[field].(string)
		if strings.TrimSpace(value) == "" {
			continue
		}
		rel := relativeTo(dir, value)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		paths = append(paths, rel)
		break
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("opencode: %s event names no path to write", e.Input.Tool)
	}
	sort.Strings(paths)
	return paths, nil
}

// relativeTo expresses a host-supplied path relative to a base directory.
// A path outside the root is returned as given, so the guard refuses it as
// unclean rather than this package silently rewriting it.
func relativeTo(root, path string) string {
	if path == "" {
		return "."
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
