// Package workflowroot guards changes to durable workflow state locations and
// writes their control-plane ownership marker. Validation remains read-only.
package workflowroot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
)

// LayoutMarkerFile is separate from the legacy text workflow-root marker.
// Initializers must write it before creating schema-2 records. Loading never
// adopts pre-existing records or upgrades their ownership implicitly.
const LayoutMarkerFile = "workflow-layout.json"

type LayoutMarker struct {
	SchemaVersion int    `json:"schema_version"`
	ConfigPath    string `json:"config_path"`
	WorkflowRoot  string `json:"workflow_root"`
	GitMode       string `json:"git_mode"`
}

// WriteLayoutMarker verifies ownership before an atomic, confined write. An
// unchanged marker is left intact, including its formatting and filesystem metadata.
func WriteLayoutMarker(marker LayoutMarker) error {
	path := filepath.Join(filepath.Dir(marker.ConfigPath), ".homonto", LayoutMarkerFile)
	if marker.SchemaVersion != 2 || !filepath.IsAbs(marker.ConfigPath) || !filepath.IsAbs(marker.WorkflowRoot) || (marker.GitMode != "existing" && marker.GitMode != "managed") {
		return fmt.Errorf("invalid workflow layout marker %q: unsupported or incomplete ownership", path)
	}
	if err := ValidateLayout(marker.ConfigPath, marker.WorkflowRoot, marker.GitMode, marker.SchemaVersion); err != nil {
		return err
	}
	repo := filepath.Dir(marker.ConfigPath)
	old, exists, err := readLayoutMarker(repo)
	if err != nil {
		return err
	}
	if exists && old == marker {
		return nil
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteControlPlaneWithin(repo, path, append(data, '\n'), 0o644); err != nil {
		return &inspectionError{"writing workflow layout marker", path, err}
	}
	return nil
}

// Absence is decided only by Lstat, never by a read through a dangling link.
func readLayoutMarker(repo string) (marker LayoutMarker, exists bool, err error) {
	path := filepath.Join(repo, ".homonto", LayoutMarkerFile)
	if err := fsutil.RequireRealParents(repo, filepath.Dir(path)); err != nil {
		return marker, false, &inspectionError{"inspecting workflow layout marker parents", path, err}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return marker, false, nil
	}
	if err != nil {
		return marker, false, &inspectionError{"inspecting workflow layout marker", path, err}
	}
	if !info.Mode().IsRegular() {
		return marker, false, fmt.Errorf("unsafe workflow layout marker %q: must be a regular file (symlinks are refused)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, true, &inspectionError{"reading workflow layout marker", path, err}
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, true, fmt.Errorf("invalid workflow layout marker %q: %w", path, err)
	}
	if marker.SchemaVersion != 2 || !filepath.IsAbs(marker.ConfigPath) || !filepath.IsAbs(marker.WorkflowRoot) || (marker.GitMode != "existing" && marker.GitMode != "managed") {
		return marker, true, fmt.Errorf("invalid workflow layout marker %q: unsupported or incomplete ownership", path)
	}
	return marker, true, nil
}

// ValidateLayout protects schema, root and Git-mode provenance as well as the
// legacy root-change contract. configPath and workflowRoot may be absolute.
func ValidateLayout(configPath, workflowRoot, gitMode string, schemaVersion int) error {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	repo := filepath.Dir(configPath)
	want := resolveRoot(repo, workflowRoot)
	wantRel, err := filepath.Rel(repo, want)
	if err != nil {
		return err
	}
	marker := filepath.Join(repo, ".homonto", LayoutMarkerFile)
	old, exists, err := readLayoutMarker(repo)
	if err != nil {
		return err
	}
	if exists {
		if old.ConfigPath != configPath {
			return fmt.Errorf("workflow layout marker %q belongs to config %q, not %q", marker, old.ConfigPath, configPath)
		}
		if schemaVersion >= 2 && old.WorkflowRoot == want && old.GitMode == gitMode {
			return ValidateChange(repo, wantRel)
		}
	} else if schemaVersion < 2 {
		return ValidateChange(repo, wantRel)
	}
	if err := ValidateChange(repo, wantRel); err != nil {
		return err
	}
	roots := []string{filepath.Join(repo, "docs"), want}
	if old.WorkflowRoot != "" {
		roots = append(roots, old.WorkflowRoot)
	}
	legacyMarker := filepath.Join(repo, ".homonto", "workflow-root")
	legacy, err := os.ReadFile(legacyMarker)
	if err != nil && !os.IsNotExist(err) {
		return &inspectionError{"reading workflow root marker", legacyMarker, err}
	}
	if was := strings.TrimSpace(string(legacy)); was != "" {
		roots = append(roots, resolveRoot(repo, was))
	}
	seen := map[string]bool{}
	var blockers strings.Builder
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
			path := filepath.Join(root, name)
			if _, err := os.Lstat(path); err == nil {
				fmt.Fprintf(&blockers, "\n  %q", path)
			} else if !os.IsNotExist(err) {
				return &inspectionError{"inspecting workflow state entry", path, err}
			}
		}
	}
	if blockers.Len() != 0 {
		return fmt.Errorf("schema/layout or workflow.git reinterpretation is forbidden while workflow state exists:%s\nrestore the prior schema/layout or explicitly migrate and preserve workflow records; no state is moved or deleted automatically", blockers.String())
	}
	return nil
}

func resolveRoot(repo, root string) string {
	root = filepath.FromSlash(root)
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Join(repo, root)
}

// ValidateChange rejects a selected repository-relative root while state remains
// at a different recorded root or the legacy docs root. Callers validate the
// configured path itself before calling this read-only migration guard.
func ValidateChange(repo, wantRel string) error {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("resolving configuration repository: %w", err)
	}
	wantRel = filepath.ToSlash(wantRel)
	wantAbs := resolveRoot(repo, wantRel)
	marker := filepath.Join(repo, ".homonto", "workflow-root")
	data, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		return &inspectionError{"reading workflow root marker", marker, err}
	}
	var roots []struct{ rel, source string }
	was := filepath.ToSlash(strings.TrimSpace(string(data)))
	if was != "" && was != wantRel {
		roots = append(roots, struct{ rel, source string }{was, fmt.Sprintf("recorded by marker %q", marker)})
	}
	if wantRel != "docs" {
		roots = append(roots, struct{ rel, source string }{"docs", "legacy default docs source"})
	}
	var blockers strings.Builder
	seen := make(map[string]bool)
	for _, root := range roots {
		abs := resolveRoot(repo, root.rel)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		var entries []string
		// Lstat deliberately counts empty directories, files and dangling links,
		// without inspecting records beneath these four ownership boundaries.
		for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
			path := filepath.Join(abs, name)
			if _, err := os.Lstat(path); err == nil {
				entries = append(entries, path)
			} else if !os.IsNotExist(err) {
				return &inspectionError{"inspecting workflow state entry", path, err}
			}
		}
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&blockers, "\n  old root %q (absolute location %q; %s):", root.rel, abs, root.source)
		for _, path := range entries {
			fmt.Fprintf(&blockers, "\n    %q", path)
		}
	}
	if blockers.Len() == 0 {
		return nil
	}
	return fmt.Errorf("workflow.root changed to %q (absolute location %q) while workflow state exists:%s\nrestore the prior workflow.root or explicitly relocate and preserve workflow records, including archives, to the new root %q; no state is moved or deleted automatically", wantRel, wantAbs, blockers.String(), wantAbs)
}

// Quote the underlying diagnostic as well: PathError otherwise repeats its path
// verbatim, allowing control characters in filenames to corrupt the message.
type inspectionError struct {
	operation string
	path      string
	err       error
}

func (e *inspectionError) Error() string {
	return fmt.Sprintf("%s %q: %q", e.operation, e.path, e.err.Error())
}

func (e *inspectionError) Unwrap() error { return e.err }
