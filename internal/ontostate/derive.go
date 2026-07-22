package ontostate

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// fileHasLinePrefix reports whether any line of the file at path starts with
// prefix (after trimming leading whitespace). A missing or unreadable file is
// simply "no" — derivation treats it as absent evidence, never an error.
func fileHasLinePrefix(path, prefix string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(strings.TrimSpace(scanner.Text()), prefix) {
			return true
		}
	}
	return false
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DeriveWorkingPhase derives a change's WORKING phase from its workspace
// artifacts — the dispatcher's evidence table, ported verbatim so the skills
// consume tested Go instead of re-running a prose table per dispatch. The
// claimed st.Phase is a cache of truth; this is the derivation that
// cross-checks it. First match from the top wins (strongest evidence first):
//
//  1. archived (flag or the caller found it under archive/)      → done
//  2. design.md marked "Status: Under revision"                  → design
//  3. verification.md with a "Result: pass" line                 → close
//  4. tasks.md has ≥1 checkbox and none unchecked                → verify
//  5. design.md marked "Status: Confirmed", or a preset          → build
//  6. full: tasks.md (incomplete) or an unconfirmed design draft → design
//  7. full: proposal.md only, claimed phase open|design          → claimed
//  8. otherwise                                                  → open
//
// Rows 6–7 encode the open↔design boundary having no file signal for a full
// change: tasks.md and design.md are design deliverables, so their unfinished
// presence is design evidence, and a proposal-only workspace trusts the
// claimed phase across exactly that boundary (a claim beyond design falls to
// open — the workspace cannot support it). Missing files are absent evidence,
// never errors; the function is read-only and total.
func DeriveWorkingPhase(changeDir string, st State) string {
	if st.Archived {
		return "done"
	}
	designPath := filepath.Join(changeDir, "design.md")
	if fileHasLinePrefix(designPath, "Status: Under revision") {
		return "design"
	}
	if fileHasLinePrefix(filepath.Join(changeDir, "verification.md"), "Result: pass") {
		return "close"
	}
	if done, err := TasksAllChecked(filepath.Join(changeDir, "tasks.md")); err == nil && done {
		return "verify"
	}
	preset := st.Workflow == "fix" || st.Workflow == "tweak"
	if fileHasLinePrefix(designPath, "Status: Confirmed") || preset {
		return "build"
	}
	if fileExists(filepath.Join(changeDir, "tasks.md")) || fileExists(designPath) {
		return "design"
	}
	if fileExists(filepath.Join(changeDir, "proposal.md")) &&
		(st.Phase == "open" || st.Phase == "design") {
		return st.Phase
	}
	return "open"
}
