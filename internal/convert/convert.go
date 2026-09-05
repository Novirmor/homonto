// Package convert is the shared transaction engine behind the two workflow
// bridges: `to promote` (to → onto) and `onto demote` (onto → to). ADR 0042.
//
// One conversion owns one lineage. The active workspace carries a neutral
// control plane under .workflow/ that both frameworks read:
//
//	.workflow/lineage.json                — lineage id + current workflow
//	.workflow/events/<operation-id>.json  — one receipt per conversion
//	.workflow/snapshots/<operation-id>/<workflow>/ — the prior active bytes
//
// Invariants the engine enforces:
//
//   - Preconditions run before any write: the source must exist as a real
//     directory with valid, non-terminal state whose identity matches its
//     directory, and the target must be free. A failed precondition leaves
//     nothing behind.
//   - Completion is receipt-verified, never inferred from directory
//     existence: a retry succeeds only when the installed target holds an
//     event receipt naming this direction, source, and target.
//   - Generation is deterministic across resumes: the manifest pre-mints the
//     operation id, the target phase, and the target identity (onto id,
//     created date) before anything moves.
//   - The immediate inverse restores: converting back with no edits to the
//     generated target nor the snapshot returns the previous workspace
//     byte-for-byte (verified by digest) and appends a restore event.
//   - Snapshots exclude .workflow itself, so repeated conversions append
//     history instead of nesting it.
//
// The caller holds the locks (in promote's fixed order: the to workspace
// lock, then the shared destination lock, then — for demote — the onto
// workspace lock) across the whole run.
package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
)

const (
	// Promote converts a `to` change into a full onto change.
	Promote = "promote"
	// Demote converts an onto change into a `to` change.
	Demote = "demote"

	controlDir    = ".workflow"
	eventsDir     = "events"
	snapshotsDir  = "snapshots"
	manifestFile  = "manifest.json"
	lineageFile   = "lineage.json"
	schemaVersion = 1
)

// workflow names the two sides.
const (
	workflowTo   = "to"
	workflowOnto = "onto"
)

// side describes one end of a bridge for a direction.
type side struct {
	workflow  string // "to" | "onto"
	dir       string // "tasks" | "changes"
	stageName string // ".to-promote" | ".onto-demote"
}

type directionSpec struct {
	name string // "promote" | "demote"
	from side   // the source side
	to   side   // the destination side
}

var specs = map[string]directionSpec{
	Promote: {name: Promote, from: side{workflowTo, "tasks", ".to-promote"}, to: side{workflowOnto, "changes", ".to-promote"}},
	Demote:  {name: Demote, from: side{workflowOnto, "changes", ".onto-demote"}, to: side{workflowTo, "tasks", ".onto-demote"}},
}

// endpoint records one end of a conversion in an event receipt.
type endpoint struct {
	Workflow string `json:"workflow"`
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Digest   string `json:"digest,omitempty"`
}

// event is the durable receipt of one conversion (or restore), stored under
// .workflow/events/<operation-id>.json inside the installed target.
type event struct {
	SchemaVersion int      `json:"schemaVersion"`
	OperationID   string   `json:"operationId"`
	Direction     string   `json:"direction"`
	Restored      bool     `json:"restored,omitempty"`
	Legacy        bool     `json:"legacy,omitempty"`
	At            string   `json:"at"`
	From          endpoint `json:"from"`
	To            endpoint `json:"to"`
	Repos         []string `json:"repos,omitempty"`
	// OntoID preserves the prior native onto id across a demotion so a later
	// promote of the same lineage can reuse it.
	OntoID string `json:"ontoId,omitempty"`
	// TargetIdentity is the pre-minted identity of the generated target,
	// replayed verbatim when a crash resume regenerates files.
	TargetIdentity targetIdentity `json:"targetIdentity"`
}

type targetIdentity struct {
	Phase   string `json:"phase"`
	OntoID  string `json:"ontoId,omitempty"`
	Created string `json:"created"`
}

// lineage is the neutral identity record carried by the active workspace.
type lineage struct {
	SchemaVersion   int    `json:"schemaVersion"`
	LineageID       string `json:"lineageId"`
	Created         string `json:"created"`
	CurrentWorkflow string `json:"currentWorkflow"`
	// Events orders the event files by conversion sequence.
	Events []string `json:"events"`
}

// manifest is the staging record: operation identity, pre-minted target
// state, and every source file's hash so recovery can prove the staged bytes
// are the recorded bytes.
type manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Kind          string `json:"kind"` // "convert" | "restore"
	Direction     string `json:"direction"`
	Source        string `json:"source"`
	Target        string `json:"target"`
	OperationID   string `json:"operationId"`
	// SourceOperationID names the conversion event whose snapshot a restore
	// returns. Restore has its own operation id so its staging can resume.
	SourceOperationID string            `json:"sourceOperationId,omitempty"`
	At                string            `json:"at"`
	Lineage           lineage           `json:"lineage"`
	SourcePhase       string            `json:"sourcePhase"`
	TargetIdent       targetIdentity    `json:"targetIdentity"`
	SourceDigest      string            `json:"sourceDigest"`
	SourceHashes      map[string]string `json:"sourceHashes"`
}

// Run executes one conversion. It is the whole transaction; the CLI commands
// wrap it with gates and locks.
func Run(direction, root, source, target string, ops opid.Supplier) (created string, err error) {
	spec, ok := specs[direction]
	if !ok {
		return "", fmt.Errorf("convert: unknown direction %q", direction)
	}
	wfRoot, err := workcli.WorkflowRoot(root)
	if err != nil {
		return "", fmt.Errorf("%s: resolving workflow root: %w", direction, err)
	}
	srcDir := filepath.Join(wfRoot, spec.from.dir, source)
	tgtDir := filepath.Join(wfRoot, spec.to.dir, target)

	// Idempotent completion: receipt-verified only.
	if done, err := completedFromReceipt(spec, tgtDir, source, target); err != nil {
		return "", err
	} else if done {
		return tgtDir, nil
	}

	// An interrupted restore resumes before anything else: its source may
	// already live inside the staging directory.
	if resumed, created, err := resumeRestore(spec, root, wfRoot, srcDir, source, target, ops); err != nil {
		return "", err
	} else if resumed {
		return created, nil
	}
	// A regular conversion moves its source into staging before generating the
	// target. Resume it before source preconditions, because the source is no
	// longer active after that durable move.
	if resumed, created, err := resumeConversion(spec, wfRoot, srcDir, tgtDir, source, target); err != nil {
		return "", err
	} else if resumed {
		return created, nil
	}

	// Restore of an immediate inverse: checked before the plain preconditions
	// because a restoreable source is valid input.
	if restored, created, err := tryRestore(spec, root, wfRoot, srcDir, tgtDir, source, target, ops); err != nil {
		return "", err
	} else if restored {
		return created, nil
	}

	// Preconditions — before any write.
	if err := preconditions(spec, root, wfRoot, srcDir, tgtDir, source, target); err != nil {
		return "", err
	}

	// Resume matching staging, or stage fresh.
	stg, m, resumed, err := findOrStage(spec, root, wfRoot, srcDir, source, target, ops)
	if err != nil {
		return "", err
	}
	work := filepath.Join(stg, "work")
	if !resumed {
		if err := buildWork(spec, work, srcDir, m); err != nil {
			return "", err
		}
	}
	// Authenticate the staged snapshot before generating anything from it:
	// staging is untrusted input.
	if err := authenticate(spec, stg, m); err != nil {
		return "", err
	}
	if err := generate(spec, work, m); err != nil {
		return "", err
	}
	// No-replace install: the destination was verified free under the locks.
	if _, err := os.Lstat(tgtDir); err == nil {
		return "", fmt.Errorf("%s: target %q already exists at %s; refusing to overwrite", spec.name, target, tgtDir)
	}
	if err := os.Rename(work, tgtDir); err != nil {
		return "", fmt.Errorf("%s: installing %s: %w", spec.name, tgtDir, err)
	}
	if err := os.RemoveAll(stg); err != nil {
		return "", err
	}
	return tgtDir, nil
}

// preconditions validates everything read-only before the first write: real
// directories (no symlinks), a free target, and a valid non-terminal source
// whose state identity matches its directory.
func preconditions(spec directionSpec, root, wfRoot, srcDir, tgtDir, source, target string) error {
	for _, p := range []string{srcDir, tgtDir, stagingRoot(spec, wfRoot), filepath.Dir(srcDir), filepath.Dir(tgtDir)} {
		if err := workcli.ValidateWorkflowPath(root, p); err != nil {
			return fmt.Errorf("%s: unsafe workflow path: %w", spec.name, err)
		}
	}
	fi, err := os.Lstat(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: no such change %q under <workflow-root>/%s", spec.name, source, spec.from.dir)
		}
		return fmt.Errorf("%s: reading %s: %w", spec.name, srcDir, err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s: source %s is not a real directory", spec.name, srcDir)
	}
	if _, err := os.Lstat(tgtDir); err == nil {
		return fmt.Errorf("%s: target %q already exists at %s; refusing to overwrite", spec.name, target, tgtDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%s: reading %s: %w", spec.name, tgtDir, err)
	}
	if err := validateExistingControlPlane(spec, srcDir); err != nil {
		return fmt.Errorf("%s: unsafe source provenance: %w", spec.name, err)
	}
	return validateSource(spec, srcDir, source)
}

func validateExistingControlPlane(spec directionSpec, srcDir string) error {
	for _, name := range []string{controlDir, legacyImportDir(spec)} {
		if name == "" {
			continue
		}
		p := filepath.Join(srcDir, name)
		fi, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a real directory", p)
		}
		if err := validateRealTree(p); err != nil {
			return err
		}
	}
	return nil
}

// validateSource loads, validates, and eligibility-checks the source state
// for the direction. Identity must match the directory.
func validateSource(spec directionSpec, srcDir, source string) error {
	switch spec.name {
	case Promote:
		st, err := tostate.Load(filepath.Join(srcDir, "to-state.yaml"))
		if err != nil {
			return fmt.Errorf("promote: loading source change: %w", err)
		}
		if err := st.Validate(); err != nil {
			return fmt.Errorf("promote: invalid source change: %w", err)
		}
		if st.Change != source {
			return fmt.Errorf("promote: source state names change %q, not %q", st.Change, source)
		}
		if st.Terminal() {
			return fmt.Errorf("promote: change %q is %s — promote an active change", source, st.Phase)
		}
	case Demote:
		st, err := ontostate.LoadChange(srcDir)
		if err != nil {
			return fmt.Errorf("demote: loading source change: %w", err)
		}
		if err := st.Validate(); err != nil {
			return fmt.Errorf("demote: invalid source change: %w", err)
		}
		if st.Change != source {
			return fmt.Errorf("demote: source state names change %q, not %q", st.Change, source)
		}
		if st.Archived || st.Abandoned {
			return fmt.Errorf("demote: change %q is terminal (archived or abandoned) — demote an active change", source)
		}
		if st.Phase == "close" {
			return fmt.Errorf("demote: change %q is closed — demote an active change", source)
		}
	}
	return nil
}

// completedFromReceipt reports an idempotent success only when the installed
// target holds an event receipt naming this direction, source, and target —
// including a restore receipt, whose retry is the same success.
func completedFromReceipt(spec directionSpec, tgtDir, source, target string) (bool, error) {
	e, ok, err := latestEvent(tgtDir)
	if err != nil || !ok {
		return false, err
	}
	if e.Direction != spec.name || e.From.Name != source || e.To.Name != target {
		return false, nil
	}
	active, err := digestActive(tgtDir)
	if err != nil || active != e.To.Digest {
		return false, err
	}
	if !e.Restored {
		// A plain conversion consumed its snapshot: it must still be present
		// and hash-identical before the retry may claim success.
		snap := filepath.Join(tgtDir, controlDir, snapshotsDir, e.OperationID, spec.from.workflow)
		_, digest, err := snapshotTree(snap)
		if err != nil || digest != e.From.Digest {
			return false, nil
		}
	}
	// Do not remove the direction-wide staging root here: it may contain an
	// unrelated interrupted conversion whose source is no longer active.
	return true, nil
}

// latestEvent loads the newest event recorded in the workspace's control
// plane. Restore events are receipts too, so callers that answer retries must
// see them.
func latestEvent(dir string) (event, bool, error) {
	lin, ok, err := loadLineage(dir)
	if err != nil || !ok {
		return event{}, false, err
	}
	for i := len(lin.Events) - 1; i >= 0; i-- {
		e, ok, err := loadEvent(dir, lin.Events[i])
		if err != nil || !ok {
			return event{}, ok, err
		}
		return e, true, nil
	}
	return event{}, false, nil
}

// latestConversionEvent ignores restore receipts when looking for a snapshot
// the immediate inverse can return to.
func latestConversionEvent(dir string) (event, bool, error) {
	lin, ok, err := loadLineage(dir)
	if err != nil || !ok {
		return event{}, ok, err
	}
	for i := len(lin.Events) - 1; i >= 0; i-- {
		e, ok, err := loadEvent(dir, lin.Events[i])
		if err != nil || !ok {
			return event{}, ok, err
		}
		if !e.Restored {
			return e, true, nil
		}
	}
	return event{}, false, nil
}

func loadEvent(dir, id string) (event, bool, error) {
	p := filepath.Join(dir, controlDir, eventsDir, id+".json")
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return event{}, false, nil
	}
	if err != nil {
		return event{}, false, err
	}
	if !fi.Mode().IsRegular() {
		return event{}, false, fmt.Errorf("convert: event %s is not a regular file", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return event{}, false, err
	}
	var e event
	if err := json.Unmarshal(data, &e); err != nil {
		return event{}, false, fmt.Errorf("convert: malformed event %s: %w", p, err)
	}
	return e, true, nil
}

func loadLineage(dir string) (lineage, bool, error) {
	p := filepath.Join(dir, controlDir, lineageFile)
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return lineage{}, false, nil
	}
	if err != nil {
		return lineage{}, false, err
	}
	if !fi.Mode().IsRegular() {
		return lineage{}, false, fmt.Errorf("convert: lineage %s is not a regular file", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return lineage{}, false, err
	}
	var lin lineage
	if err := json.Unmarshal(data, &lin); err != nil {
		return lineage{}, false, fmt.Errorf("convert: malformed %s: %w", lineageFile, err)
	}
	if lin.SchemaVersion != schemaVersion {
		return lineage{}, false, fmt.Errorf("convert: lineage schema %d is not %d", lin.SchemaVersion, schemaVersion)
	}
	return lin, true, nil
}

// resumeConversion continues a conversion after its source was moved into a
// manifest-authenticated staging directory but before the target installed.
func resumeConversion(spec directionSpec, wfRoot, srcDir, tgtDir, source, target string) (bool, string, error) {
	base := stagingRoot(spec, wfRoot)
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	for _, ent := range entries {
		stg := filepath.Join(base, ent.Name())
		m, ok, err := readManifest(spec, stg)
		if err != nil || !ok {
			return false, "", err
		}
		if m.Kind != "convert" || m.Direction != spec.name || m.Source != source || m.Target != target {
			continue
		}
		if _, err := os.Lstat(filepath.Join(stg, "work")); os.IsNotExist(err) {
			// The manifest is durable before buildWork starts. With no work/
			// directory, no source bytes can have moved, so an otherwise empty
			// staging directory can safely restart from the live source.
			if !onlyManifest(stg) {
				return false, "", fmt.Errorf("%s: staging %s has no work/ and holds unexpected entries; inspect manually", spec.name, stg)
			}
			if err := os.RemoveAll(stg); err != nil {
				return false, "", err
			}
			return false, "", nil
		} else if err != nil {
			return false, "", err
		}
		snap := filepath.Join(stg, "work", controlDir, snapshotsDir, m.OperationID, spec.from.workflow)
		if _, err := os.Lstat(snap); os.IsNotExist(err) {
			if _, srcErr := os.Lstat(srcDir); srcErr == nil && onlyPreMoveStaging(stg) {
				if err := os.RemoveAll(stg); err != nil {
					return false, "", err
				}
				return false, "", nil
			}
			return false, "", fmt.Errorf("%s: staging %s has no source snapshot; inspect manually", spec.name, stg)
		} else if err != nil {
			return false, "", err
		}
		if err := authenticate(spec, stg, m); err != nil {
			return false, "", err
		}
		for _, f := range generatedFiles(spec) {
			_ = os.Remove(filepath.Join(stg, "work", f))
		}
		work := filepath.Join(stg, "work")
		if err := generate(spec, work, m); err != nil {
			return false, "", err
		}
		if _, err := os.Lstat(tgtDir); err == nil {
			return false, "", fmt.Errorf("%s: target %q already exists at %s; refusing to overwrite", spec.name, target, tgtDir)
		} else if !os.IsNotExist(err) {
			return false, "", err
		}
		if err := os.Rename(work, tgtDir); err != nil {
			return false, "", fmt.Errorf("%s: installing %s: %w", spec.name, tgtDir, err)
		}
		if err := os.RemoveAll(stg); err != nil {
			return false, "", err
		}
		return true, tgtDir, nil
	}
	return false, "", nil
}

func onlyManifest(stg string) bool {
	entries, err := os.ReadDir(stg)
	if err != nil || len(entries) != 1 || entries[0].Name() != manifestFile {
		return false
	}
	fi, err := os.Lstat(filepath.Join(stg, manifestFile))
	return err == nil && fi.Mode().IsRegular()
}

// onlyPreMoveStaging permits restart after a crash between buildWork creating
// its empty directory skeleton and its atomic source rename. No regular file
// other than the manifest means no source bytes can have entered staging.
func onlyPreMoveStaging(stg string) bool {
	return filepath.WalkDir(stg, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if p == filepath.Join(stg, manifestFile) && d.Type().IsRegular() {
			return nil
		}
		return fmt.Errorf("unexpected staging entry")
	}) == nil
}

func stagingRoot(spec directionSpec, wfRoot string) string {
	return filepath.Join(wfRoot, spec.from.stageName)
}

// findOrStage returns the staging directory and manifest for this
// conversion, resuming a matching interrupted one.
func findOrStage(spec directionSpec, root, wfRoot, srcDir, source, target string, ops opid.Supplier) (string, manifest, bool, error) {
	base := stagingRoot(spec, wfRoot)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", manifest{}, false, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", manifest{}, false, err
	}
	for _, e := range entries {
		stg := filepath.Join(base, e.Name())
		m, ok, err := readManifest(spec, stg)
		if err != nil {
			return "", manifest{}, false, err
		}
		if !ok {
			continue
		}
		if m.Direction != spec.name || m.Source != source || m.Target != target {
			// Unrelated staging: leave for its own retry or the user.
			continue
		}
		if m.Kind == "restore" {
			// resumeRestore owns restore staging. Never discard a workspace whose
			// source may already have moved into it.
			continue
		}
		// Matching staging: authenticate before resuming.
		if err := authenticate(spec, stg, m); err != nil {
			return "", manifest{}, false, err
		}
		// Drop stale generated files; imported bytes are verified by hash.
		for _, f := range generatedFiles(spec) {
			_ = os.Remove(filepath.Join(stg, "work", f))
		}
		return stg, m, true, nil
	}
	// Fresh staging: pre-mint every value a resume must replay verbatim.
	opID := ops.NewID()
	now := time.Now().UTC()
	srcPhase, _, srcOntoID, err := sourceFacts(spec, srcDir)
	if err != nil {
		return "", manifest{}, false, err
	}
	lin := lineage{SchemaVersion: schemaVersion, LineageID: ops.NewID(), Created: now.Format("2006-01-02"), CurrentWorkflow: spec.to.workflow}
	if prior, ok, err := loadLineage(srcDir); err != nil {
		return "", manifest{}, false, err
	} else if ok {
		lin.LineageID = prior.LineageID
		lin.Created = prior.Created
		lin.Events = prior.Events
	}
	tid := targetIdentity{
		Phase:   targetPhase(spec, srcPhase, srcDir),
		Created: now.Format("2006-01-02"),
	}
	if spec.name == Promote {
		tid.OntoID = ontostate.NewID()
	} else if srcOntoID != "" {
		tid.OntoID = srcOntoID
	}
	hashes, digest, err := snapshotTree(srcDir, legacyImportDir(spec))
	if err != nil {
		return "", manifest{}, false, err
	}
	m := manifest{
		SchemaVersion: schemaVersion,
		Kind:          "convert",
		Direction:     spec.name,
		Source:        source,
		Target:        target,
		OperationID:   opID,
		At:            now.Format(time.RFC3339),
		Lineage:       lin,
		SourcePhase:   srcPhase,
		TargetIdent:   tid,
		SourceDigest:  digest,
		SourceHashes:  hashes,
	}
	stg := filepath.Join(base, opID)
	if err := os.Mkdir(stg, 0o700); err != nil {
		return "", manifest{}, false, fmt.Errorf("%s: staging: %w", spec.name, err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", manifest{}, false, err
	}
	if err := writeFileExclusive(filepath.Join(stg, manifestFile), data); err != nil {
		return "", manifest{}, false, err
	}
	return stg, m, false, nil
}

// sourceFacts extracts the phase, selected repos, and (for demote) the
// native onto id of the source.
func sourceFacts(spec directionSpec, srcDir string) (phase string, repos []string, ontoID string, err error) {
	switch spec.name {
	case Promote:
		st, err := tostate.Load(filepath.Join(srcDir, "to-state.yaml"))
		if err != nil {
			return "", nil, "", err
		}
		return st.Phase, st.Repos, "", nil
	case Demote:
		st, err := ontostate.LoadChange(srcDir)
		if err != nil {
			return "", nil, "", err
		}
		return st.Phase, st.Repos, st.ID, nil
	}
	return "", nil, "", fmt.Errorf("convert: unknown direction %q", spec.name)
}

// targetPhase maps the source phase onto the destination workflow's phases.
// Demotion into do requires the onto tasks to translate into a contract-
// clean `to` plan; otherwise the change restarts at plan.
func targetPhase(spec directionSpec, srcPhase, srcDir string) string {
	if spec.name == Promote {
		return "open" // promotion claims no design or verification
	}
	switch srcPhase {
	case "build", "verify":
		if planTranslatable(srcDir) {
			return tostate.PhaseDo
		}
		return tostate.PhasePlan
	default:
		return tostate.PhasePlan
	}
}

// buildWork assembles the future target directory in staging: the source
// workspace moves whole (one atomic rename) under the control plane's
// snapshots, then any prior control plane (or legacy imported-* history) is
// lifted out flat so history appends instead of nesting.
func buildWork(spec directionSpec, work, srcDir string, m manifest) error {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	ctl := filepath.Join(work, controlDir)
	snapRoot := filepath.Join(ctl, snapshotsDir)
	if err := os.MkdirAll(snapRoot, 0o755); err != nil {
		return err
	}

	// 1. One atomic move: the source becomes this operation's snapshot.
	snap := filepath.Join(snapRoot, m.OperationID, spec.from.workflow)
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		return err
	}
	if err := os.Rename(srcDir, snap); err != nil {
		return fmt.Errorf("%s: moving the source workspace (is <workflow-root>/%s on the same filesystem?): %w", spec.name, spec.from.dir, err)
	}

	// 2. Lift the source's own control plane out (flat adoption).
	priorCtl := filepath.Join(snap, controlDir)
	if fi, err := os.Lstat(priorCtl); err == nil && fi.IsDir() {
		if err := validateRealTree(priorCtl); err != nil {
			return fmt.Errorf("%s: unsafe prior control plane: %w", spec.name, err)
		}
		if err := moveContents(priorCtl, ctl); err != nil {
			return fmt.Errorf("%s: adopting prior history: %w", spec.name, err)
		}
		_ = os.Remove(priorCtl)
	}

	// 3. Adopt legacy v0.18 imported-* provenance as a legacy snapshot; the
	// active bytes the manifest hashed exclude it, so it must leave the
	// snapshot before authentication.
	if legacy := legacyImportDir(spec); legacy != "" {
		old := filepath.Join(snap, legacy)
		if fi, err := os.Lstat(old); err == nil && fi.IsDir() {
			if err := validateRealTree(old); err != nil {
				return fmt.Errorf("%s: unsafe legacy provenance: %w", spec.name, err)
			}
			legacyID := m.OperationID + "-legacy"
			dst := filepath.Join(snapRoot, legacyID, spec.from.workflow)
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			if err := os.Rename(old, filepath.Join(dst, legacy)); err != nil {
				return fmt.Errorf("%s: adopting legacy %s: %w", spec.name, legacy, err)
			}
		}
	}
	return nil
}

// legacyImportDir names the v0.18 provenance directory a source may carry.
func legacyImportDir(spec directionSpec) string {
	if spec.name == Demote {
		return "imported-to" // an onto change promoted before the lineage store
	}
	return "imported-onto" // a to change demoted before the lineage store
}

// moveContents moves every entry of src into dst (both must exist),
// merging recursively when an entry exists as a directory on both sides.
func moveContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		sfi, err := os.Lstat(s)
		if err != nil {
			return err
		}
		if !sfi.IsDir() && !sfi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a real directory or regular file", s)
		}
		if fi, err := os.Lstat(d); err == nil && fi.IsDir() && sfi.IsDir() {
			if err := moveContents(s, d); err != nil {
				return err
			}
			continue
		}
		if err := os.Rename(s, d); err != nil {
			return err
		}
	}
	return nil
}

// validateRealTree rejects any non-directory/non-regular object before the
// control plane is moved into a location later written by the engine. In
// particular this prevents a carried lineage or receipt symlink from turning
// a subsequent os.WriteFile into an outside-workspace write.
func validateRealTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type().IsRegular() {
			return nil
		}
		return fmt.Errorf("%s is not a real directory or regular file", p)
	})
}

// generate (re)writes the canonical target state and documents from the
// snapshot plus the manifest's pre-minted identity. Deterministic given the
// manifest.
func generate(spec directionSpec, work string, m manifest) error {
	snap := filepath.Join(work, controlDir, snapshotsDir, m.OperationID, spec.from.workflow)
	// The event receipt is written before the framework files so an
	// interrupted generate is regenerated wholesale anyway.
	switch spec.name {
	case Promote:
		st, err := tostate.Load(filepath.Join(snap, "to-state.yaml"))
		if err != nil {
			return fmt.Errorf("promote: regenerating onto state: %w", err)
		}
		plan := ""
		if b, err := os.ReadFile(filepath.Join(snap, "plan.md")); err == nil {
			plan = string(b)
		}
		ost := ontostate.State{
			Change:   m.Target,
			ID:       m.TargetIdent.OntoID,
			Workflow: "full",
			Phase:    m.TargetIdent.Phase,
			Created:  m.TargetIdent.Created,
			Repos:    st.Repos,
		}
		if err := ontostate.Save(filepath.Join(work, "onto-state.yaml"), ost); err != nil {
			return err
		}
		proposal := buildProposal(m, st.Phase, plan)
		if err := writeFileExclusive(filepath.Join(work, "proposal.md"), []byte(proposal)); err != nil {
			return err
		}
	case Demote:
		st, err := ontostate.LoadChange(snap)
		if err != nil {
			return fmt.Errorf("demote: regenerating to state: %w", err)
		}
		tst := tostate.State{
			Change:  m.Target,
			Phase:   m.TargetIdent.Phase,
			Created: m.TargetIdent.Created,
			Repos:   st.Repos,
		}
		if err := tostate.Save(filepath.Join(work, "to-state.yaml"), tst); err != nil {
			return err
		}
		plan := buildToPlan(m, st.Phase, snap)
		if err := writeFileExclusive(filepath.Join(work, "plan.md"), []byte(plan)); err != nil {
			return err
		}
	}
	return writeReceipt(spec, work, m)
}

// writeReceipt finalizes the control plane: lineage, the event receipt, and
// — for the demote direction — the recorded prior onto id.
func writeReceipt(spec directionSpec, work string, m manifest) error {
	ctl := filepath.Join(work, controlDir)
	tgtDigest, err := digestActive(work)
	if err != nil {
		return err
	}
	lin := m.Lineage
	if len(lin.Events) == 0 {
		lin.Events = []string{}
	}
	lin.Events = append(lin.Events, m.OperationID)
	lin.CurrentWorkflow = spec.to.workflow
	data, err := json.MarshalIndent(lin, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(ctl, eventsDir), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ctl, lineageFile), data, 0o644); err != nil {
		return err
	}
	_, repos, ontoID, err := sourceFacts(spec, filepath.Join(ctl, snapshotsDir, m.OperationID, spec.from.workflow))
	if err != nil {
		return err
	}
	e := event{
		SchemaVersion:  schemaVersion,
		OperationID:    m.OperationID,
		Direction:      spec.name,
		At:             m.At,
		From:           endpoint{Workflow: spec.from.workflow, Name: m.Source, Phase: m.SourcePhase, Digest: m.SourceDigest},
		To:             endpoint{Workflow: spec.to.workflow, Name: m.Target, Phase: m.TargetIdent.Phase, Digest: tgtDigest},
		Repos:          repos,
		OntoID:         ontoID,
		TargetIdentity: m.TargetIdent,
	}
	// Legacy adoption is noted when a legacy snapshot was relocated.
	if _, err := os.Lstat(filepath.Join(ctl, snapshotsDir, m.OperationID+"-legacy")); err == nil {
		e.Legacy = true
	}
	edata, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ctl, eventsDir, m.OperationID+".json"), edata, 0o644)
}

// authenticate verifies a staging directory end to end: regular owned
// objects only, expected layout, real directories, and the staged snapshot
// hashing exactly to the manifest's recorded bytes.
func authenticate(spec directionSpec, stg string, m manifest) error {
	for p := stg; p != filepath.Dir(p); p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("%s: staging %s unreadable: %w", spec.name, p, err)
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: %s is not a real directory; refusing tampered staging", spec.name, p)
		}
	}
	entries, err := os.ReadDir(stg)
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != manifestFile+",work" {
		return fmt.Errorf("%s: staging %s holds unexpected entries %v; refusing", spec.name, stg, names)
	}
	wfi, err := os.Lstat(filepath.Join(stg, "work"))
	if err != nil || !wfi.IsDir() {
		return fmt.Errorf("%s: staging %s has no real work/ directory; refusing", spec.name, stg)
	}
	snap := filepath.Join(stg, "work", controlDir, snapshotsDir, m.OperationID, spec.from.workflow)
	got, digest, err := snapshotTree(snap)
	if err != nil {
		return err
	}
	// The snapshot must hold the source's state file at all — an empty or
	// gutted tree with an empty manifest hash set must not authenticate.
	for _, state := range sourceStateFiles(spec) {
		if _, ok := got[state]; ok {
			break
		}
		return fmt.Errorf("%s: staged snapshot has no %s; refusing tampered staging", spec.name, state)
	}
	for path, want := range m.SourceHashes {
		if got[path] != want {
			return fmt.Errorf("%s: staged %s does not match its manifest hash; refusing tampered staging", spec.name, path)
		}
	}
	if len(got) != len(m.SourceHashes) {
		return fmt.Errorf("%s: staged tree holds %d files, manifest recorded %d; refusing", spec.name, len(got), len(m.SourceHashes))
	}
	_ = digest
	return nil
}

// sourceStateFiles names the state files a staged snapshot of this
// direction's source must contain (LoadChange accepts the legacy name).
func sourceStateFiles(spec directionSpec) []string {
	if spec.name == Promote {
		return []string{"to-state.yaml"}
	}
	return []string{"onto-state.yaml", "state.yaml"}
}

// readManifest loads a staging manifest; ok=false when the directory has
// none (interrupted before the manifest write — safe to remove and redo).
func readManifest(spec directionSpec, stg string) (manifest, bool, error) {
	mpath := filepath.Join(stg, manifestFile)
	fi, err := os.Lstat(mpath)
	if os.IsNotExist(err) {
		// Half-created staging: no manifest, nothing moved yet. Remove it
		// only when it cannot hold user bytes.
		if onlySafeScratch(stg) {
			_ = os.RemoveAll(stg)
		}
		return manifest{}, false, nil
	}
	if err != nil {
		return manifest{}, false, err
	}
	if !fi.Mode().IsRegular() {
		return manifest{}, false, fmt.Errorf("%s: staging manifest %s is not a regular file; refusing", spec.name, mpath)
	}
	data, err := os.ReadFile(mpath)
	if err != nil {
		return manifest{}, false, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, false, fmt.Errorf("%s: malformed manifest in %s: %w", spec.name, stg, err)
	}
	if m.SchemaVersion != schemaVersion {
		return manifest{}, false, fmt.Errorf("%s: manifest schema %d in %s is not %d", spec.name, m.SchemaVersion, stg, schemaVersion)
	}
	if m.OperationID != filepath.Base(stg) {
		return manifest{}, false, fmt.Errorf("%s: staging %s does not match its manifest operation id %q; refusing", spec.name, stg, m.OperationID)
	}
	return m, true, nil
}

// onlySafeScratch reports whether an unmanifested staging tree holds
// nothing beyond empty scratch structure — the only case auto-removal is
// allowed. Anything else is left for manual recovery.
func onlySafeScratch(stg string) bool {
	entries, err := os.ReadDir(stg)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == "work" && e.IsDir() {
			inner, err := os.ReadDir(filepath.Join(stg, "work"))
			if err != nil || len(inner) != 0 {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func writeFileExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("convert: writing %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// snapshotTree digests every regular file under root, keyed by
// slash-relative path, EXCLUDING the .workflow control plane (whose contents
// are receipts, not active bytes) and any top-level names in skipTop (the
// legacy provenance directories, which adoption relocates). Symlinks and
// other irregular objects are refused — conversions move real bytes only.
func snapshotTree(root string, skipTop ...string) (map[string]string, string, error) {
	out := map[string]string{}
	skip := map[string]bool{}
	for _, s := range skipTop {
		if s != "" {
			skip[s] = true
		}
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == controlDir && p != root {
				return filepath.SkipDir
			}
			if p != root && skip[d.Name()] && filepath.Dir(p) == root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("convert: %s inside a converted workspace is not a regular file", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return out, digestOf(out), nil
}

// digestActive hashes the generated target's active files (the same
// exclusion applies) into one stable digest string.
func digestActive(root string) (string, error) {
	_, digest, err := snapshotTree(root)
	return digest, err
}

// digestOf folds a hash set into one deterministic digest.
func digestOf(files map[string]string) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write([]byte(files[n]))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
