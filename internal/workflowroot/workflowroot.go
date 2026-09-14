// Package workflowroot guards changes to durable workflow state locations and
// writes their control-plane ownership marker. Validation remains read-only.
package workflowroot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/migrationrecord"
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

// LegacyMigrationReadError reports a stable, non-content diagnostic for the
// narrowly supported read-only legacy ownership shape. Callers must not expose
// its wrapped error in machine-readable migration output.
type LegacyMigrationReadError struct {
	Code string
	err  error
}

func (e *LegacyMigrationReadError) Error() string { return e.err.Error() }
func (e *LegacyMigrationReadError) Unwrap() error { return e.err }

func legacyMigrationReadError(code string, err error) error {
	return &LegacyMigrationReadError{Code: code, err: err}
}

// LegacyMigrationReadCode returns a safe classification for a failed
// ValidateLegacyMigrationRead call. It intentionally never returns parser or
// filesystem error text.
func LegacyMigrationReadCode(err error) string {
	var diagnostic *LegacyMigrationReadError
	if errors.As(err, &diagnostic) && diagnostic.Code != "" {
		return diagnostic.Code
	}
	return "legacy_owner_invalid"
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
		return applylock.PrepareGuardianRoot(filepath.Join(repo, ".homonto"))
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteControlPlaneWithin(repo, path, append(data, '\n'), 0o644); err != nil {
		return &inspectionError{"writing workflow layout marker", path, err}
	}
	return applylock.PrepareGuardianRoot(filepath.Join(repo, ".homonto"))
}

// TransitionLegacyMigrationLayout is the one write path that can activate
// schema-2 ownership over the supported legacy-records shape. Ordinary marker
// writes still go through WriteLayoutMarker and ValidateLayout, which continue
// to reject reinterpretation while records exist. The migration executor calls
// this only after its records commits have succeeded; the pending private
// journal, public receipt, and backward-looking commit proof are mandatory
// preconditions rather than a force flag.
func TransitionLegacyMigrationLayout(marker LayoutMarker, runID string) error {
	if marker.SchemaVersion != 2 || marker.GitMode != "existing" || !filepath.IsAbs(marker.ConfigPath) || filepath.Clean(marker.ConfigPath) != marker.ConfigPath || !filepath.IsAbs(marker.WorkflowRoot) || filepath.Clean(marker.WorkflowRoot) != marker.WorkflowRoot {
		return fmt.Errorf("invalid legacy migration layout transition")
	}
	if !migrationrecord.SafeRunID(runID) {
		return fmt.Errorf("invalid legacy migration run ID")
	}
	repo := filepath.Dir(marker.ConfigPath)
	if err := fsutil.RequireRealParents(repo, filepath.Dir(marker.ConfigPath)); err != nil {
		return fmt.Errorf("legacy migration layout transition: unsafe config path: %w", err)
	}
	info, err := os.Lstat(marker.ConfigPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("legacy migration layout transition: config must remain a real regular file")
	}
	if _, exists, err := readLayoutMarker(repo); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("legacy migration layout transition: layout marker already exists")
	}
	if err := ValidateLegacyMigrationRead(marker.ConfigPath, marker.WorkflowRoot, marker.GitMode, marker.SchemaVersion); err != nil {
		return fmt.Errorf("legacy migration layout transition: %w", err)
	}
	status, err := migrationrecord.LoadJournalStatus(marker.WorkflowRoot, runID)
	if err != nil || status.Phase != "pending-finalization" {
		return fmt.Errorf("legacy migration layout transition: migration is not ready for finalization")
	}
	if _, err := migrationrecord.LoadReceipt(marker.WorkflowRoot, runID); err != nil {
		return fmt.Errorf("legacy migration layout transition: public receipt is invalid: %w", err)
	}
	if _, err := migrationrecord.LoadCommitProof(marker.WorkflowRoot, runID); err != nil {
		return fmt.Errorf("legacy migration layout transition: commit proof is invalid: %w", err)
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(repo, ".homonto", LayoutMarkerFile)
	if err := fsutil.WriteControlPlaneWithin(repo, path, append(data, '\n'), 0o644); err != nil {
		return &inspectionError{"writing legacy migration layout marker", path, err}
	}
	return applylock.PrepareGuardianRoot(filepath.Join(repo, ".homonto"))
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
			if err := migrationrecord.ValidateBarrier(want); err != nil {
				return err
			}
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

// ValidateLegacyMigrationRead validates the one legacy ownership shape that a
// read-only schema-2 migration planner may inventory. It deliberately does not
// call ValidateLayout: that guard must continue refusing the reinterpretation
// for every ordinary command. This function writes nothing and never adopts a
// marker or records directory.
func ValidateLegacyMigrationRead(configPath, workflowRoot, gitMode string, schemaVersion int) error {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if schemaVersion != 2 || gitMode != "existing" || !filepath.IsAbs(workflowRoot) || filepath.Clean(workflowRoot) != workflowRoot {
		return legacyMigrationReadError("precondition_failed", fmt.Errorf("workspace migration requires schema-2 existing-Git ownership"))
	}
	repo := filepath.Dir(configPath)
	if _, exists, err := readLayoutMarker(repo); err != nil {
		return legacyMigrationReadError("layout_marker_invalid", err)
	} else if exists {
		return legacyMigrationReadError("layout_marker_present", fmt.Errorf("workspace migration requires legacy ownership; workflow layout marker %q is already present", filepath.Join(repo, ".homonto", LayoutMarkerFile)))
	}
	legacy, exists, err := readLegacyRootMarker(repo)
	if err != nil {
		return legacyMigrationReadError("legacy_marker_invalid", err)
	}
	if !exists {
		return legacyMigrationReadError("legacy_marker_missing", fmt.Errorf("workspace migration requires legacy workflow root marker %q", filepath.Join(repo, ".homonto", "workflow-root")))
	}
	if filepath.Clean(resolveRoot(repo, legacy)) != filepath.Clean(workflowRoot) {
		return legacyMigrationReadError("legacy_marker_mismatch", fmt.Errorf("legacy workflow root marker %q does not match configured workflow root", filepath.Join(repo, ".homonto", "workflow-root")))
	}
	wantRel, err := filepath.Rel(repo, workflowRoot)
	if err != nil {
		return legacyMigrationReadError("legacy_alternative_root_uninspectable", fmt.Errorf("resolving configured workflow root: %w", err))
	}
	if err := validateChange(repo, filepath.ToSlash(wantRel), false); err != nil {
		return legacyMigrationReadError("legacy_alternative_state_present", err)
	}
	return nil
}

// ValidateLegacyMigrationRecovery validates the current ownership markers for
// an interrupted legacy migration without treating a pending journal as a
// reason to skip schema, root, or marker checks. It is intentionally narrower
// than normal loading, whose barrier must continue rejecting pending work.
func ValidateLegacyMigrationRecovery(configPath, workflowRoot, gitMode string, schemaVersion int) error {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if schemaVersion != 2 || gitMode != "existing" || !filepath.IsAbs(workflowRoot) || filepath.Clean(workflowRoot) != workflowRoot {
		return fmt.Errorf("workspace migration recovery requires schema-2 existing-Git ownership")
	}
	repo := filepath.Dir(configPath)
	marker, exists, err := readLayoutMarker(repo)
	if err != nil {
		return err
	}
	if exists {
		if marker.SchemaVersion != 2 || marker.ConfigPath != configPath || marker.WorkflowRoot != workflowRoot || marker.GitMode != gitMode {
			return fmt.Errorf("workspace migration recovery layout marker does not match the configured workspace")
		}
		return nil
	}
	legacy, exists, err := readLegacyRootMarker(repo)
	if err != nil {
		return err
	}
	if !exists || filepath.Clean(resolveRoot(repo, legacy)) != filepath.Clean(workflowRoot) {
		return fmt.Errorf("workspace migration recovery legacy root marker does not match the configured workflow root")
	}
	wantRel, err := filepath.Rel(repo, workflowRoot)
	if err != nil {
		return err
	}
	return validateChange(repo, filepath.ToSlash(wantRel), false)
}

func readLegacyRootMarker(repo string) (root string, exists bool, err error) {
	path := filepath.Join(repo, ".homonto", "workflow-root")
	if err := fsutil.RequireRealParents(repo, filepath.Dir(path)); err != nil {
		return "", false, &inspectionError{"inspecting legacy workflow root marker parents", path, err}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, &inspectionError{"inspecting legacy workflow root marker", path, err}
	}
	if !info.Mode().IsRegular() {
		return "", true, fmt.Errorf("unsafe legacy workflow root marker %q: must be a regular file (symlinks are refused)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", true, &inspectionError{"reading legacy workflow root marker", path, err}
	}
	root = strings.TrimSpace(string(data))
	if root == "" || strings.ContainsAny(root, "\x00\r\n\t") {
		return "", true, fmt.Errorf("invalid legacy workflow root marker %q", path)
	}
	return root, true, nil
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
	return validateChange(repo, wantRel, true)
}

// validateChange shares the legacy-root inspection used by ordinary root
// changes and migration inventory. Migration inventory permits state at the
// selected legacy root, so it skips only the recorded-root reinterpretation
// check while retaining the alternate docs-root boundary checks.
func validateChange(repo, wantRel string, inspectRecordedRoot bool) error {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("resolving configuration repository: %w", err)
	}
	wantRel = filepath.ToSlash(wantRel)
	wantAbs := resolveRoot(repo, wantRel)
	marker := filepath.Join(repo, ".homonto", "workflow-root")
	var roots []struct{ rel, source string }
	if inspectRecordedRoot {
		data, err := os.ReadFile(marker)
		if err != nil && !os.IsNotExist(err) {
			return &inspectionError{"reading workflow root marker", marker, err}
		}
		was := filepath.ToSlash(strings.TrimSpace(string(data)))
		if was != "" && was != wantRel {
			roots = append(roots, struct{ rel, source string }{was, fmt.Sprintf("recorded by marker %q", marker)})
		}
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
