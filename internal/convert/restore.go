package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/opid"
)

// tryRestore converts back to the immediately-prior workflow when nothing
// has changed since: the generated target's active digest still matches the
// event's to-digest and the snapshot still hashes to the event's
// from-digest. The snapshot returns byte-for-byte as the active workspace,
// the freshly generated files are discarded, and a restore event is
// appended. Anything else falls through to a normal conversion.
func tryRestore(spec directionSpec, root, wfRoot, srcDir, tgtDir, source, target string, ops opid.Supplier) (bool, string, error) {
	lin, ok, err := loadLineage(srcDir)
	if err != nil || !ok {
		return false, "", err
	}
	e, ok, err := latestConversionEvent(srcDir)
	if err != nil || !ok {
		return false, "", err
	}
	// Exact inverse only: this operation undoes that event, --as must repeat
	// the original name.
	if e.Direction == spec.name || e.Restored || e.Legacy {
		return false, "", nil
	}
	if e.To.Workflow != spec.from.workflow || e.To.Name != source || e.From.Workflow != spec.to.workflow || e.From.Name != target {
		return false, "", nil
	}
	cur, err := digestActive(srcDir)
	if err != nil || cur != e.To.Digest {
		return false, "", nil
	}
	snap := filepath.Join(srcDir, controlDir, snapshotsDir, e.OperationID, spec.to.workflow)
	if _, err := os.Lstat(snap); err != nil {
		return false, "", nil
	}
	hashes, digest, err := snapshotTree(snap)
	if err != nil || len(hashes) == 0 || digest != e.From.Digest {
		return false, "", nil
	}
	// The ordinary preconditions would reject this too, but check before
	// creating restore staging so an occupied target cannot leave a manifest
	// that looks resumable while the source remains active.
	if _, err := os.Lstat(tgtDir); err == nil {
		return false, "", nil
	} else if !os.IsNotExist(err) {
		return false, "", err
	}

	// Stage the restore so every intermediate state lives under one
	// resumable directory.
	base := stagingRoot(spec, wfRoot)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return false, "", err
	}
	restoreID := ops.NewID()
	stg := filepath.Join(base, restoreID)
	if err := os.Mkdir(stg, 0o700); err != nil {
		return false, "", fmt.Errorf("%s: staging restore: %w", spec.name, err)
	}
	m := manifest{
		SchemaVersion:     schemaVersion,
		Kind:              "restore",
		Direction:         spec.name,
		Source:            source,
		Target:            target,
		OperationID:       restoreID,
		SourceOperationID: e.OperationID,
		SourceDigest:      e.From.Digest,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, "", err
	}
	if err := writeFileExclusive(filepath.Join(stg, manifestFile), data); err != nil {
		return false, "", err
	}

	created, err := finishRestore(spec, stg, m, lin, e, wfRoot)
	if err != nil {
		return false, "", err
	}
	return true, created, nil
}

// finishRestore performs the resumable move sequence: the whole source moves
// into staging, the snapshot returns as the active workspace, the control
// plane follows with a restore event appended, and the result installs with
// one atomic rename. Every step is idempotent within the staging directory,
// so a retry re-enters safely.
func finishRestore(spec directionSpec, stg string, m manifest, lin lineage, e event, wfRoot string) (string, error) {
	work := filepath.Join(stg, "work")
	restored := filepath.Join(stg, "restored")
	srcDir := filepath.Join(wfRoot, spec.from.dir, e.To.Name)
	tgt := filepath.Join(wfRoot, spec.to.dir, e.From.Name)

	// Refuse an occupied destination before moving the active source into
	// staging. A collision must leave the source active and untouched.
	if _, err := os.Lstat(tgt); err == nil {
		return "", fmt.Errorf("%s: restore target %q already exists at %s; inspect %s manually", spec.name, e.From.Name, tgt, stg)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	// 1. Move the whole current workspace into staging (once).
	if fi, err := os.Lstat(srcDir); err == nil && fi.IsDir() {
		if _, err := os.Lstat(work); err == nil {
			return "", fmt.Errorf("%s: interrupted restore staging is inconsistent; inspect %s manually", spec.name, stg)
		}
		if err := os.Rename(srcDir, work); err != nil {
			return "", fmt.Errorf("%s: staging the source for restore: %w", spec.name, err)
		}
	}
	if _, err := os.Lstat(work); err != nil {
		return "", fmt.Errorf("%s: restore staging lost its source; inspect %s manually", spec.name, stg)
	}

	// 2. Authenticate the complete original snapshot before returning any part
	// of it. A resumed restore may have already moved some entries into
	// restored/, so the digest covers both trees as one logical snapshot.
	if err := authenticateRestore(stg, m, e); err != nil {
		return "", err
	}

	// The snapshot becomes the restored active bytes.
	if err := os.MkdirAll(restored, 0o755); err != nil {
		return "", err
	}
	snap := filepath.Join(work, controlDir, snapshotsDir, m.SourceOperationID, spec.to.workflow)
	if err := moveContents(snap, restored); err != nil {
		return "", fmt.Errorf("%s: restoring the snapshot: %w", spec.name, err)
	}

	// 3. The control plane follows, flat, with a restore event appended.
	ctl := filepath.Join(restored, controlDir)
	if err := os.MkdirAll(filepath.Join(ctl, eventsDir), 0o755); err != nil {
		return "", err
	}
	if prior := filepath.Join(work, controlDir); prior != ctl {
		if fi, err := os.Lstat(prior); err == nil && fi.IsDir() {
			if err := validateRealTree(prior); err != nil {
				return "", fmt.Errorf("%s: unsafe staged control plane: %w", spec.name, err)
			}
			if err := moveContents(prior, ctl); err != nil {
				return "", fmt.Errorf("%s: carrying history across the restore: %w", spec.name, err)
			}
		}
	}
	updated := lin
	updated.CurrentWorkflow = spec.to.workflow
	re := event{
		SchemaVersion:  schemaVersion,
		OperationID:    m.OperationID,
		Direction:      spec.name,
		Restored:       true,
		At:             e.At,
		From:           e.To,
		To:             e.From,
		Repos:          e.Repos,
		OntoID:         e.OntoID,
		TargetIdentity: e.TargetIdentity,
	}
	updated.Events = append(updated.Events, re.OperationID)
	if err := writeJSON(filepath.Join(ctl, lineageFile), updated); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(ctl, eventsDir, re.OperationID+".json"), re); err != nil {
		return "", err
	}

	// 4. Atomic install, then the staging goes away.
	if err := os.Rename(restored, tgt); err != nil {
		return "", fmt.Errorf("%s: installing the restored workspace: %w", spec.name, err)
	}
	if err := os.RemoveAll(stg); err != nil {
		return "", err
	}
	return tgt, nil
}

func authenticateRestore(stg string, m manifest, e event) error {
	if m.SourceOperationID == "" || m.SourceDigest == "" || m.SourceDigest != e.From.Digest {
		return fmt.Errorf("convert: restore staging %s lacks the expected source digest", stg)
	}
	work := filepath.Join(stg, "work")
	snap := filepath.Join(work, controlDir, snapshotsDir, m.SourceOperationID, e.From.Workflow)
	parts := []string{snap, filepath.Join(stg, "restored")}
	combined := map[string]string{}
	for _, part := range parts {
		if _, err := os.Lstat(part); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		hashes, _, err := snapshotTree(part)
		if err != nil {
			return fmt.Errorf("convert: authenticating restore %s: %w", stg, err)
		}
		for name, hash := range hashes {
			if _, exists := combined[name]; exists {
				return fmt.Errorf("convert: restore staging %s duplicates %s across its snapshot", stg, name)
			}
			combined[name] = hash
		}
	}
	if len(combined) == 0 || digestOf(combined) != m.SourceDigest {
		return fmt.Errorf("convert: restore staging %s does not match its source receipt", stg)
	}
	return nil
}

// resumeRestore continues an interrupted restore whose staging directory
// holds a matching manifest. Returns ("", nil) when the staging does not
// match, letting the caller fall through to preconditions.
func resumeRestore(spec directionSpec, root, wfRoot, srcDir, source, target string, ops opid.Supplier) (bool, string, error) {
	base := stagingRoot(spec, wfRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	for _, ent := range entries {
		stg := filepath.Join(base, ent.Name())
		m, ok, err := readManifest(spec, stg)
		if err != nil || !ok {
			return false, "", err
		}
		if m.Kind != "restore" || m.Direction != spec.name || m.Source != source || m.Target != target {
			continue
		}
		work := filepath.Join(stg, "work")
		if _, err := os.Lstat(work); os.IsNotExist(err) {
			if !onlyManifest(stg) {
				return false, "", fmt.Errorf("%s: interrupted restore %s has no source and unexpected entries; inspect manually", spec.name, stg)
			}
			if err := os.RemoveAll(stg); err != nil {
				return false, "", err
			}
			return false, "", nil
		} else if err != nil {
			return false, "", err
		}
		lin, ok, err := loadLineage(work)
		if err != nil || !ok {
			return false, "", fmt.Errorf("%s: interrupted restore %s lost its history; inspect manually", spec.name, stg)
		}
		e, ok, err := eventByID(work, m.SourceOperationID)
		if err != nil || !ok {
			return false, "", fmt.Errorf("%s: interrupted restore %s lost its receipt; inspect manually", spec.name, stg)
		}
		created, err := finishRestore(spec, stg, m, lin, e, wfRoot)
		if err != nil {
			return false, "", err
		}
		return true, created, nil
	}
	return false, "", nil
}

func eventByID(dir, id string) (event, bool, error) {
	if id == "" {
		return event{}, false, nil
	}
	return loadEvent(dir, id)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
