// Package promote converts a `to` change into a full onto change (ADR 0028):
// a fresh proposal-only workspace in phase open, with the complete source
// workspace moved unchanged under imported-to/. The caller holds BOTH locks
// (the `to` workspace lock, then the shared destination lock) across the
// whole run. Crash recovery is idempotent: a staging directory with a
// matching manifest is resumed, generated files are regenerated (never
// trusted from staging), and tampered or unrelated staging is refused.
package promote

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

// stagingRoot is the promotion staging area under the config repo.
func stagingRoot(root string) string {
	return filepath.Join(workcli.WorkflowRootOrDefault(root), ".to-promote")
}

// manifest is the staging record: what is being promoted, from where, with
// every source file's hash so recovery can prove the staged bytes are the
// recorded bytes.
type manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Source        string            `json:"source"`
	Target        string            `json:"target"`
	OperationID   string            `json:"operationId"`
	SourceHashes  map[string]string `json:"sourceHashes"`
}

const manifestVersion = 1

// Run promotes docs/tasks/<source> into docs/changes/<target>. It is the
// whole conversion; the tocli command wraps it with locks and next-step
// output. Source artifacts move byte-for-byte; onto state and the proposal
// are generated deterministically and regenerated on any retry.
func Run(root, source, target string, ops opid.Supplier) (created string, err error) {
	workflowRoot := workcli.WorkflowRootOrDefault(root)
	srcDir := filepath.Join(workflowRoot, "tasks", source)
	tgtDir := filepath.Join(workflowRoot, "changes", target)

	// Already finished? A complete promotion (source gone, target present,
	// matching staging cleaned) is an idempotent success on retry.
	if ok, err := completed(root, source, target, srcDir, tgtDir); err != nil {
		return "", err
	} else if ok {
		return tgtDir, nil
	}

	// Resume matching staging, or create fresh. Staging is checked BEFORE
	// source loading: an interrupted promotion has already moved the source,
	// so the resume path must not require it. Only a fresh promotion needs
	// its source present.
	stg, resumed, err := findOrStage(root, source, target, srcDir, ops)
	if err != nil {
		return "", err
	}
	if !resumed {
		if _, err := os.Stat(srcDir); err != nil {
			return "", fmt.Errorf("promote: no such `to` change %q under <workflow-root>/tasks", source)
		}
	}
	if !resumed {
		if _, err := os.Stat(tgtDir); err == nil {
			return "", fmt.Errorf("promote: target %q already exists at %s; refusing to overwrite", target, tgtDir)
		}
	}
	work := filepath.Join(stg, "work")
	if !resumed {
		st, err := tostate.Load(filepath.Join(srcDir, "to-state.yaml"))
		if err != nil {
			return "", fmt.Errorf("promote: loading source change: %w", err)
		}
		if st.Terminal() {
			return "", fmt.Errorf("promote: change %q is %s — promote an active change", source, st.Phase)
		}
		if err := buildWork(work, target, st, srcDir, filepath.Join(workflowRoot, "tasks")); err != nil {
			return "", err
		}
	}
	// Always regenerate the generated files: staging is untrusted input. The
	// imported to-state.yaml is authoritative for identity fields on both the
	// fresh and resumed paths (buildWork moved it in before generate runs).
	if err := generate(work, target); err != nil {
		return "", err
	}
	if err := os.Rename(work, tgtDir); err != nil {
		return "", fmt.Errorf("promote: installing %s: %w", tgtDir, err)
	}
	if err := os.RemoveAll(stg); err != nil {
		return "", err
	}
	return tgtDir, nil
}

// completed reports an idempotent success: target exists, source is gone,
// and any staging manifest names this exact promotion.
func completed(root, source, target, srcDir, tgtDir string) (bool, error) {
	if _, err := os.Stat(srcDir); err == nil {
		return false, nil // source still present: not finished
	}
	if _, err := os.Stat(tgtDir); err != nil {
		return false, nil
	}
	// Source gone and target present: clean up matching staging, refuse
	// foreign leftovers.
	entries, _ := os.ReadDir(stagingRoot(root))
	for _, e := range entries {
		mpath := filepath.Join(stagingRoot(root), e.Name(), "manifest.json")
		data, err := os.ReadFile(mpath)
		if err != nil {
			continue
		}
		var m manifest
		if json.Unmarshal(data, &m) != nil {
			return false, fmt.Errorf("promote: unreadable staging manifest at %s", mpath)
		}
		if m.Source == source && m.Target == target {
			_ = os.RemoveAll(filepath.Join(stagingRoot(root), e.Name()))
		}
	}
	return true, nil
}

// findOrStage returns the staging directory for this promotion, resuming a
// matching interrupted one. Staging is authorized only when every parent is
// a real directory, the manifest is regular and matches source/target, the
// staged tree contains only manifest.json, work/, and expected names, and
// work/imported-to hashes match the manifest. Generated files inside work/
// are deleted (regenerated); tampering with imported bytes refuses.
func findOrStage(root, source, target, srcDir string, ops opid.Supplier) (string, bool, error) {
	base := stagingRoot(root)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", false, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		stg := filepath.Join(base, e.Name())
		m, ok, err := readManifest(stg)
		if err != nil {
			return "", false, err
		}
		if !ok || m.Source != source || m.Target != target {
			continue // unrelated staging: leave for its own retry or the user
		}
		// Matching staging: authenticate before resuming.
		if err := authenticate(stg, m, source, target); err != nil {
			return "", false, err
		}
		// Drop stale generated files; imported bytes are verified by hash.
		for _, f := range []string{"onto-state.yaml", "proposal.md"} {
			_ = os.Remove(filepath.Join(stg, "work", f))
		}
		return stg, true, nil
	}

	// Fresh staging: mode-0700 create-only directory named by operation ID.
	stg := filepath.Join(base, ops.NewID())
	if err := os.Mkdir(stg, 0o700); err != nil {
		return "", false, fmt.Errorf("promote: staging: %w", err)
	}
	hashes, err := hashTree(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			hashes = map[string]string{} // missing root: Run reports "no such change"
		} else {
			return "", false, err
		}
	}
	m := manifest{SchemaVersion: manifestVersion, Source: source, Target: target, OperationID: ops.NewID(), SourceHashes: hashes}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", false, err
	}
	if err := writeExclusive(filepath.Join(stg, "manifest.json"), data); err != nil {
		return "", false, err
	}
	return stg, false, nil
}

// authenticate verifies a resumed staging directory end to end: regular
// owned objects only, expected layout, and imported-to hashing exactly to the
// manifest's recorded bytes.
func authenticate(stg string, m manifest, source, target string) error {
	// Parents must be real directories (no symlinked redirects).
	for p := stg; p != filepath.Dir(p); p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("promote: staging %s unreadable: %w", p, err)
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("promote: %s is not a real directory; refusing tampered staging", p)
		}
	}
	// Layout: exactly manifest.json and work/ (imported-to is inside work).
	entries, err := os.ReadDir(stg)
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "manifest.json,work" {
		return fmt.Errorf("promote: staging %s holds unexpected entries %v; refusing", stg, names)
	}
	// Imported bytes must hash to the manifest.
	imp := filepath.Join(stg, "work", "imported-to")
	got, err := hashTree(imp)
	if err != nil {
		return err
	}
	for path, want := range m.SourceHashes {
		if got[path] != want {
			return fmt.Errorf("promote: staged %s does not match its manifest hash; refusing tampered staging", path)
		}
	}
	if len(got) != len(m.SourceHashes) {
		return fmt.Errorf("promote: staged tree holds %d files, manifest recorded %d; refusing", len(got), len(m.SourceHashes))
	}
	return nil
}

// buildWork assembles the future change directory in staging: the source
// workspace moved (renamed, byte-identical) under imported-to/.
func buildWork(work, target string, st tostate.State, srcDir, tasksRoot string) error {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	if err := os.Rename(srcDir, filepath.Join(work, "imported-to")); err != nil {
		return fmt.Errorf("promote: moving the source workspace: %w (is <workflow-root>/tasks on the same filesystem?)", err)
	}
	return nil
}

// generate (re)writes the canonical onto state and proposal from the
// imported to-state.yaml and plan.md. Deterministic; never trusted from
// staging.
func generate(work, target string) error {
	src := filepath.Join(work, "imported-to")
	st, err := tostate.Load(filepath.Join(src, "to-state.yaml"))
	if err != nil {
		return fmt.Errorf("promote: regenerating onto state: %w", err)
	}
	plan := ""
	if b, err := os.ReadFile(filepath.Join(src, "plan.md")); err == nil {
		plan = string(b)
	}
	ost := ontostate.State{
		Change:   target,
		ID:       ontostate.NewID(),
		Workflow: "full",
		Phase:    "open",
		Created:  time.Now().UTC().Format("2006-01-02"),
		Repos:    st.Repos,
	}
	if err := ontostate.Save(filepath.Join(work, "onto-state.yaml"), ost); err != nil {
		return err
	}
	proposal := buildProposal(target, st, plan)
	return writeExclusive(filepath.Join(work, "proposal.md"), []byte(proposal))
}

func buildProposal(name string, st tostate.State, plan string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Proposal: %s (promoted from `to`)\n\n", name)
	fmt.Fprintf(&b, "Promoted from the `to` change recorded under `imported-to/`.\n")
	fmt.Fprintf(&b, "The promotion does not claim design or verification happened —\n")
	fmt.Fprintf(&b, "this change starts at phase open with a fresh proposal.\n\n")
	fmt.Fprintf(&b, "- **Imported phase**: %s\n", st.Phase)
	if len(st.Repos) > 0 {
		fmt.Fprintf(&b, "- **Selected repos**: %s\n", strings.Join(st.Repos, ", "))
	}
	if head := firstMeaningful(plan); head != "" {
		fmt.Fprintf(&b, "- **Plan excerpt (from the imported plan.md)**: %s\n", head)
	}
	b.WriteString("\n## Why promoted\n\n<fill in: what grew beyond `to`'s shape — design questions, evidence\nobligations, a second reader>\n")
	return b.String()
}

func firstMeaningful(plan string) string {
	for _, ln := range strings.Split(plan, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") {
			continue
		}
		if len(t) > 160 {
			t = t[:160] + "…"
		}
		return t
	}
	return ""
}

// readManifest loads a staging manifest; ok=false when the directory has
// none (interrupted before the manifest write — safe to remove and redo).
func readManifest(stg string) (manifest, bool, error) {
	data, err := os.ReadFile(filepath.Join(stg, "manifest.json"))
	if os.IsNotExist(err) {
		// Half-created staging: no manifest, nothing moved yet. Remove it so
		// a fresh attempt can proceed.
		if onlyDirsOrEmpty(stg) {
			_ = os.RemoveAll(stg)
		}
		return manifest{}, false, nil
	}
	if err != nil {
		return manifest{}, false, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, false, fmt.Errorf("promote: malformed manifest in %s: %w", stg, err)
	}
	if m.SchemaVersion != manifestVersion {
		return manifest{}, false, fmt.Errorf("promote: manifest schema %d in %s is not %d", m.SchemaVersion, stg, manifestVersion)
	}
	return m, true, nil
}

func onlyDirsOrEmpty(stg string) bool {
	entries, err := os.ReadDir(stg)
	if err != nil {
		return false
	}
	if len(entries) == 0 {
		return true
	}
	// Only work/ (empty) and no files: safe.
	for _, e := range entries {
		if e.Name() == "work" && e.IsDir() {
			continue
		}
		return false
	}
	return true
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("promote: writing %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// hashTree digests every regular file under root, keyed by slash-relative
// path. Symlinks are refused (promotion moves real bytes only).
func hashTree(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("promote: symlink %s inside a promoted workspace is not expected", p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out, err
}
