package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// evidenceFindings validates a change's structured evidence against its
// artifacts (G6): each current claim's task number must exist in tasks.md, its
// scenario ID must exist in the workflow's contract, its commit must still be reachable
// (a rebase or squash makes the record stale), and verification.md must hash
// to the recorded artifact hash. Ambiguous scenario declarations are findings
// even without a sidecar; the absence of a sidecar itself is only a legacy note.
func evidenceFindings(cmd *cobra.Command, root, changeDir, name string) (findings, notes []string) {
	st, stateErr := ontostate.LoadChange(changeDir)
	scenarios, scenarioErr := loadScenarioIndex(changeDir, st)
	findings = scenarioFindings(name, scenarios)
	if scenarioErr != nil {
		findings = append(findings, fmt.Sprintf("%s: evidence scenario contract: %v", name, scenarioErr))
	}
	sc, ok, err := evidence.Load(name, evidence.Path(changeDir))
	if err != nil {
		return append(findings, fmt.Sprintf("%s: evidence sidecar unusable: %v", name, err)), nil
	}
	if !ok {
		// Legacy note — only for a change that actually reached verification,
		// where evidence would live had sidecars existed. A change still in
		// open/design/build has nothing to note, and doctor's healthy verdict
		// must stay exactly "healthy" for it.
		if _, statErr := os.Stat(filepath.Join(changeDir, "verification.md")); statErr == nil {
			return findings, []string{fmt.Sprintf("note: %s verified without an evidence sidecar (pre-v0.15.0 change; evidence is optional)", name)}
		}
		return findings, nil
	}

	// Requirement IDs retain their independent uniqueness check.
	seenReqIDs := map[string]string{}
	dupes := []string{}
	specs, _ := deltaSpecPaths(filepath.Join(changeDir, "specs"))
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
	var sources map[string]string
	if stateErr == nil {
		sources, stateErr = stateSourceDirs(root, st)
	}
	if stateErr != nil {
		findings = append(findings, fmt.Sprintf("%s: evidence source scope: %v", name, stateErr))
	}
	latest := latestEvidence(sc.Records)
	for i, rec := range sc.Records {
		label := fmt.Sprintf("%s evidence[%d]", name, i+1)
		if seenOps[rec.OperationID] {
			findings = append(findings, fmt.Sprintf("%s: duplicate operation %s", label, rec.OperationID))
		}
		seenOps[rec.OperationID] = true
		if latest[evidenceKey(rec)] != i {
			continue
		}
		if !taskNums[rec.Task] {
			findings = append(findings, fmt.Sprintf("%s: task #%d not in tasks.md (stale record)", label, rec.Task))
		}
		if len(scenarios[rec.Scenario]) == 0 {
			findings = append(findings, fmt.Sprintf("%s: scenario %q not found in any delta spec or no-spec preset scenario contract (stale or orphaned record)", label, rec.Scenario))
		}
		source, scoped := sources[rec.Repo]
		if !scoped {
			findings = append(findings, fmt.Sprintf("%s: repository %q is not in source scope", label, rec.Repo))
		}
		if (rec.Commit != "" && (!scoped || !commitReachable(cmd, source, rec.Commit))) || (st.RepoMode == "explicit" && (rec.Commit == "" || isAncestor(source, rec.Commit, "HEAD") != nil)) {
			findings = append(findings, fmt.Sprintf("%s: commit %s unreachable (rebased or squashed; record a fresh verification)", label, short(rec.Commit)))
		}
		if rec.ArtifactHash != "" && rec.ArtifactHash != verHash {
			findings = append(findings, fmt.Sprintf("%s: verification.md changed since the record (stale evidence; re-verify)", label))
		}
	}
	return findings, nil
}

// commitReachable reports whether a commit object still exists in the
// repository at root (best-effort: no git or no repo means "reachable" — the
// check is evidence staleness, not git health).
func commitReachable(cmd *cobra.Command, root, commit string) bool {
	_, err := resolveCommit(root, commit)
	return err == nil
}

var _ = strings.TrimSpace
