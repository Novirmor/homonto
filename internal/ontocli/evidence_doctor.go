package ontocli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/spf13/cobra"
)

// evidenceFindings validates a change's structured evidence against its
// artifacts (G6): every record's task number must exist in tasks.md, its
// scenario ID must exist in a delta spec, its commit must still be reachable
// (a rebase or squash makes the record stale), and verification.md must hash
// to the recorded artifact hash. Duplicate scenario IDs in the specs are a
// finding — ambiguous evidence is no evidence. A change with no sidecar is
// legacy: a printed note, never a finding.
func evidenceFindings(cmd *cobra.Command, root, changeDir, name string) (findings, notes []string) {
	sc, ok, err := evidence.Load(name, evidence.Path(changeDir))
	if err != nil {
		return []string{fmt.Sprintf("%s: evidence sidecar unusable: %v", name, err)}, nil
	}
	if !ok {
		// Legacy note — only for a change that actually reached verification,
		// where evidence would live had sidecars existed. A change still in
		// open/design/build has nothing to note, and doctor's healthy verdict
		// must stay exactly "healthy" for it.
		if _, statErr := os.Stat(filepath.Join(changeDir, "verification.md")); statErr == nil {
			return nil, []string{fmt.Sprintf("note: %s verified without an evidence sidecar (pre-v0.15.0 change; evidence is optional)", name)}
		}
		return nil, nil
	}

	// Index the delta specs' scenario IDs and requirement IDs.
	scenarioIDs := map[string]bool{}
	seenReqIDs := map[string]string{}
	dupes := []string{}
	specs, _ := filepath.Glob(filepath.Join(changeDir, "specs", "*.md"))
	for _, spec := range specs {
		data, err := os.ReadFile(spec)
		if err != nil {
			continue
		}
		for _, r := range evidence.ParseRequirements(string(data)) {
			if r.ID != "" {
				if prev, clash := seenReqIDs[r.ID]; clash {
					dupes = append(dupes, fmt.Sprintf("%s used by %q and %q", r.ID, prev, r.Name))
				} else {
					seenReqIDs[r.ID] = r.Name
				}
			}
			for _, s := range r.Scenarios {
				if s.ID != "" {
					scenarioIDs[s.ID] = true
				}
			}
		}
	}
	for _, d := range dupes {
		findings = append(findings, fmt.Sprintf("%s: duplicate Requirement-ID %s", name, d))
	}

	// Index tasks.md numbers.
	taskNums := map[int]bool{}
	if data, err := os.ReadFile(filepath.Join(changeDir, "tasks.md")); err == nil {
		for _, t := range evidence.ParseTasks(string(data)) {
			taskNums[t.Number] = true
		}
	}

	// Current verification.md hash.
	verHash := ""
	if h, err := hashFile(filepath.Join(changeDir, "verification.md")); err == nil {
		verHash = h
	}

	seenOps := map[string]bool{}
	for i, rec := range sc.Records {
		label := fmt.Sprintf("%s evidence[%d]", name, i+1)
		if !taskNums[rec.Task] {
			findings = append(findings, fmt.Sprintf("%s: task #%d not in tasks.md (stale record)", label, rec.Task))
		}
		if !scenarioIDs[rec.Scenario] {
			findings = append(findings, fmt.Sprintf("%s: scenario %q not found in any delta spec (stale or orphaned record)", label, rec.Scenario))
		}
		if rec.Commit != "" && !commitReachable(cmd, root, rec.Commit) {
			findings = append(findings, fmt.Sprintf("%s: commit %s unreachable (rebased or squashed; record a fresh verification)", label, short(rec.Commit)))
		}
		if rec.ArtifactHash != "" && verHash != "" && rec.ArtifactHash != verHash {
			findings = append(findings, fmt.Sprintf("%s: verification.md changed since the record (stale evidence; re-verify)", label))
		}
		if seenOps[rec.OperationID] {
			findings = append(findings, fmt.Sprintf("%s: duplicate operation %s", label, rec.OperationID))
		}
		seenOps[rec.OperationID] = true
	}
	return findings, nil
}

// commitReachable reports whether a commit object still exists in the
// repository at root (best-effort: no git or no repo means "reachable" — the
// check is evidence staleness, not git health).
func commitReachable(cmd *cobra.Command, root, commit string) bool {
	c := exec.Command("git", "-C", root, "cat-file", "-e", commit+"^{commit}")
	return c.Run() == nil
}

var _ = strings.TrimSpace
