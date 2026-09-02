// Package handoff defines the versioned recovery envelope shared by the onto
// and `to` workflow tools (ADR 0027). Two views exist: the interactive view a
// session prints (which may carry full native state) and the persisted
// recovery view, which carries an explicit field allowlist and never artifact
// prose or free-form state — a user can paste a secret into a plan, so
// anything written under the workspace must be metadata only.
package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the recovery-envelope format version this binary writes.
// Loaders reject unknown major versions fail-closed.
const SchemaVersion = 1

// GateRef is one pending machine gate, referenced by ID only. SetArgv is the
// argv template that records the answer (values may be "<value>" placeholders).
type GateRef struct {
	ID      string   `json:"id"`
	Header  string   `json:"header,omitempty"`
	SetArgv []string `json:"setArgv,omitempty"`
}

// ArtifactDigest names one workspace artifact and its content hash, so a
// resuming session can detect staleness without re-reading prose.
type ArtifactDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Recovery is the persisted recovery view: everything a fresh session needs
// to re-ground, and nothing it could not safely commit. Free-form state
// (directives, evidence text, plan excerpts) is excluded by construction.
type Recovery struct {
	SchemaVersion int              `json:"schemaVersion"`
	Tool          string           `json:"tool"`
	Change        string           `json:"change"`
	OperationID   string           `json:"operationId"`
	Generated     string           `json:"generated"`
	Workflow      string           `json:"workflow,omitempty"`
	Phase         string           `json:"phase"`
	DerivedPhase  string           `json:"derivedPhase,omitempty"`
	PhaseMismatch bool             `json:"phaseMismatch,omitempty"`
	Deps          []string         `json:"deps,omitempty"`
	RepoAliases   []string         `json:"repoAliases,omitempty"`
	BaseRef       string           `json:"baseRef,omitempty"`
	HeadCommit    string           `json:"headCommit,omitempty"`
	PendingGates  []GateRef        `json:"pendingGates,omitempty"`
	Artifacts     []ArtifactDigest `json:"artifacts,omitempty"`
	NextArgv      []string         `json:"nextArgv,omitempty"`
}

// ValidateSchema rejects an envelope this binary cannot understand. Unknown
// major versions are a hard error: guessing at a newer format's fields would
// silently drop recovery data.
func ValidateSchema(v int) error {
	if v <= 0 {
		return fmt.Errorf("handoff: envelope has no schemaVersion")
	}
	if v > SchemaVersion {
		return fmt.Errorf("handoff: envelope schemaVersion %d is newer than this binary supports (%d) — upgrade the tool: %w", v, SchemaVersion, os.ErrInvalid)
	}
	return nil
}

// Markdown renders the metadata-only text pack. It re-derives every line from
// the envelope's fields, so no artifact prose can leak through formatting.
func Markdown(r Recovery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s handoff: %s (recovery pack)\n\n", r.Tool, r.Change)
	fmt.Fprintf(&b, "- **operation**: %s\n- **generated**: %s\n", r.OperationID, r.Generated)
	if r.Workflow != "" {
		fmt.Fprintf(&b, "- **workflow**: %s\n", r.Workflow)
	}
	fmt.Fprintf(&b, "- **phase**: %s", r.Phase)
	if r.DerivedPhase != "" && r.DerivedPhase != r.Phase {
		fmt.Fprintf(&b, " (derived: %s — MISMATCH)", r.DerivedPhase)
	} else if r.DerivedPhase != "" {
		fmt.Fprintf(&b, " (derived: %s)", r.DerivedPhase)
	}
	b.WriteString("\n")
	if len(r.Deps) > 0 {
		fmt.Fprintf(&b, "- **deps**: %s\n", strings.Join(r.Deps, ", "))
	}
	if len(r.RepoAliases) > 0 {
		fmt.Fprintf(&b, "- **repos**: %s\n", strings.Join(r.RepoAliases, ", "))
	}
	if r.BaseRef != "" {
		fmt.Fprintf(&b, "- **base_ref**: %s\n", r.BaseRef)
	}
	if r.HeadCommit != "" {
		fmt.Fprintf(&b, "- **head_commit**: %s\n", r.HeadCommit)
	}
	if len(r.PendingGates) > 0 {
		b.WriteString("\n## Pending decisions\n\n")
		for _, g := range r.PendingGates {
			if len(g.SetArgv) > 0 {
				fmt.Fprintf(&b, "- **%s** — record with `%s`\n", g.ID, strings.Join(g.SetArgv, " "))
			} else {
				fmt.Fprintf(&b, "- **%s**\n", g.ID)
			}
		}
	}
	if len(r.Artifacts) > 0 {
		b.WriteString("\n## Artifacts (hash only — read the file for content)\n\n")
		for _, a := range r.Artifacts {
			fmt.Fprintf(&b, "- `%s` — sha256:%s\n", a.Path, a.SHA256)
		}
	}
	if len(r.NextArgv) > 0 {
		fmt.Fprintf(&b, "\nNext: `%s`\n", strings.Join(r.NextArgv, " "))
	}
	b.WriteString("\nRe-derive the phase from file state before acting; this pack carries identity, not prose.\n")
	return b.String()
}

// WritePack persists the recovery JSON and Markdown under dir with
// create-only, no-follow semantics: every path component from root to dir
// must be a real directory (a symlinked parent could redirect the write
// outside the workspace), and neither destination may already exist. The
// operation ID makes the filenames unique; an existing file with the same
// name is a collision, not something to overwrite.
func WritePack(root, dir string, r Recovery, jsonBytes, markdown []byte) (jsonPath, mdPath string, err error) {
	if err := ValidateSchema(r.SchemaVersion); err != nil {
		return "", "", err
	}
	if err := requireRealDir(root, dir); err != nil {
		return "", "", err
	}
	base := fmt.Sprintf("%s-context", r.OperationID)
	jsonPath = filepath.Join(dir, base+".json")
	mdPath = filepath.Join(dir, base+".md")
	for _, p := range []string{jsonPath, mdPath} {
		if _, err := os.Lstat(p); err == nil {
			return "", "", fmt.Errorf("handoff: %s already exists; refusing to overwrite", p)
		}
	}
	if err := writeFileExclusive(jsonPath, jsonBytes); err != nil {
		return "", "", err
	}
	if err := writeFileExclusive(mdPath, markdown); err != nil {
		return "", "", err
	}
	return jsonPath, mdPath, nil
}

// requireRealDir verifies dir sits under root and that every component from
// root through dir is a real directory — no symlinks anywhere on the path.
func requireRealDir(root, dir string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("handoff: pack directory %s is outside the workspace root %s", dir, root)
	}
	cur := absRoot
	// Walk each component; Lstat catches symlinked intermediates.
	comps := strings.Split(filepath.ToSlash(rel), "/")
	for _, c := range comps {
		if c == "." || c == "" {
			continue
		}
		cur = filepath.Join(cur, c)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				// The final handoff directory may need creating — create it
				// real (MkdirAll after the check above proved the parents).
				if err := os.MkdirAll(absDir, 0o755); err != nil {
					return fmt.Errorf("handoff: create %s: %w", absDir, err)
				}
				return nil
			}
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("handoff: %s is not a real directory (symlinked parents are refused)", cur)
		}
	}
	return nil
}

func writeFileExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("handoff: write %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Stamp fills the generated timestamp in RFC3339 (UTC).
func Stamp(r *Recovery, t time.Time) {
	r.Generated = t.UTC().Format(time.RFC3339)
}
