package ontostate

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
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
	// A scan error means the evidence could not be read — same answer as a
	// missing file: absent, never an error.
	_ = scanner.Err()
	return false
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// VerificationResultLine returns the only Result: line in the file at path.
// Duplicate markers are contradictory evidence, so they return ok=false just
// like a missing, unreadable, or malformed report.
func VerificationResultLine(path string) (line string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(trimmed, "Result:") {
			if ok {
				return "", false
			}
			line, ok = trimmed, true
		}
	}
	if scanner.Err() != nil {
		return "", false
	}
	return line, ok
}

var passingResultLine = regexp.MustCompile(`^Result: pass(?: \([1-9][0-9]* accepted deviations\))?$`)

// ResultLineIsPass reports whether a verification Result line is one of the
// two canonical pass forms. Free-form suffixes and template placeholders do
// not count as evidence.
func ResultLineIsPass(line string) bool {
	return passingResultLine.MatchString(line)
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
		if !ArchiveIntegrationComplete(changeDir, st) {
			return "close"
		}
		return "done"
	}
	// An abandoned change is retired — deriving a live working phase for it
	// would only decorate status rows with spurious mismatches. Echo the
	// claim; discovery skips abandoned changes anyway.
	if st.Abandoned {
		return st.Phase
	}
	designPath := filepath.Join(changeDir, "design.md")
	if fileHasLinePrefix(designPath, "Status: Under revision") {
		return "design"
	}
	if line, ok := VerificationResultLine(filepath.Join(changeDir, "verification.md")); st.Verify.Result == "pass" && ok && ResultLineIsPass(line) {
		return "close"
	}
	// A preset-to-full upgrade explicitly returns to design. Completed preset
	// tasks are not allowed to route around the new design obligation.
	if st.Workflow == "full" && st.Observed.PresetEscalated && st.ApproachConfirmed == "" {
		return "design"
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
