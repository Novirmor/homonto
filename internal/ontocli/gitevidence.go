package ontocli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

// fullCommitID matches the canonical object ids the capture path writes.
var fullCommitID = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// inGitRepository reports whether dir sits inside a working git repository.
func inGitRepository(dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	return gitAt(ctx, dir, "rev-parse", "--git-common-dir").Run() == nil
}

// captureVerifyHeads freezes each scoped repository's HEAD (alias "" is the
// config repository) at the moment a verification pass is recorded.
func captureVerifyHeads(root string, st ontostate.State) (map[string]string, error) {
	st.Verify.Result = "pending"
	st.Verify.Heads = nil
	heads := map[string]string{}
	dirs, err := stateSourceDirs(root, st)
	if err != nil {
		return nil, err
	}
	for name, dir := range dirs {
		head, err := resolveCommit(dir, "HEAD")
		if err != nil {
			return nil, err
		}
		heads[name] = head
	}
	return heads, nil
}

// verifyHeadsIntact refuses a close whose repositories moved past the frozen
// verification heads in ways the workflow cannot explain: the head must remain
// an ancestor of HEAD, and every path changed since may only be workflow
// bookkeeping. In declared repositories even bookkeeping is unexpected — the
// workflow keeps no tree there — so any change refuses.
func verifyHeadsIntact(root string, st ontostate.State) error {
	repoDirs, err := stateSourceDirs(root, st)
	if err != nil {
		return err
	}
	for alias, head := range st.Verify.Heads {
		dir := repoDirs[alias]
		display := alias
		if display == "" {
			display = "config"
		}
		if dir == "" {
			return fmt.Errorf("repository %s recorded a verification head but is no longer in scope", display)
		}
		// Captured heads are always canonical object ids; anything else is a
		// hand-shaped value that could alias a moving rev ("HEAD") and re-bind
		// itself at close time.
		if !fullCommitID.MatchString(head) {
			return fmt.Errorf("repository %s: verification head %q is not a canonical commit id; re-verify the change", display, head)
		}
		canonical, err := resolveCommit(dir, head)
		if err != nil {
			return fmt.Errorf("repository %s: verification head %s no longer resolves; re-verify the change", display, head)
		}
		if err := isAncestor(dir, canonical, "HEAD"); err != nil {
			return fmt.Errorf("repository %s: verification is stale (verified commit is no longer in history); re-verify the change", display)
		}
		if st.RepoMode == "explicit" {
			current, err := resolveCommit(dir, "HEAD")
			if err != nil {
				return err
			}
			if err := integrationCandidateIntact(root, dir, integrationrecord.Entry{SourceCommit: canonical}, current); err != nil {
				return fmt.Errorf("repository %s changed after verification: %w; re-verify the change", display, err)
			}
			continue
		}
		changed, err := changedPaths(dir, canonical, "HEAD")
		if err != nil {
			return fmt.Errorf("repository %s: %w", display, err)
		}
		prefix, combined := recordsGitPrefix(root, dir)
		for _, path := range changed {
			if alias != "" && (!combined || !isRecordsBookkeeping(prefix, path)) {
				return fmt.Errorf("repository %s changed after verification (%s); re-verify the change", display, path)
			}
			if alias == "" && !isWorkflowBookkeeping(root, path) {
				return fmt.Errorf("source path %s changed after the verification pass; re-verify the change", path)
			}
		}
	}
	return nil
}

func isRecordsBookkeeping(rel, path string) bool {
	for _, name := range []string{"changes", "specs", "adr", "guides"} {
		if strings.HasPrefix(path, filepath.ToSlash(filepath.Join(rel, name))+"/") {
			return true
		}
	}
	return false
}

// Git paths are worktree-root-relative, even when the declared source/config
// is a subdirectory. A separate nested records repository is not this owner.
func recordsGitPrefix(root, source string) (string, bool) {
	top, err := gitWorktreeRoot(source)
	if err != nil {
		return "", false
	}
	owner, err := gitWorktreeRoot(workflowRoot(root))
	if err != nil || owner != top {
		return "", false
	}
	records, err := filepath.Abs(workflowRoot(root))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(top, records)
	return rel, err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func gitWorktreeRoot(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := gitAt(ctx, dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimSpace(string(out)))
}

func isWorkflowBookkeeping(root, changedPath string) bool {
	workflowRel, err := filepath.Rel(root, workflowRoot(root))
	if err != nil {
		return false
	}
	for _, name := range []string{"changes", "specs", "adr", "guides"} {
		prefix := filepath.ToSlash(filepath.Join(workflowRel, name)) + "/"
		if strings.HasPrefix(changedPath, prefix) {
			return true
		}
	}
	return strings.HasPrefix(changedPath, ".homonto/")
}

// changedPaths lists the paths that differ between two commits. --no-renames
// keeps a rename out of docs/ from hiding the deletion of its source (the
// same hole the dirt scan closes), and -z keeps whitespace-bearing names
// whole.
func changedPaths(dir, from, to string) ([]string, error) {
	for _, rev := range []string{from, to} {
		if strings.HasPrefix(rev, "-") {
			return nil, fmt.Errorf("invalid revision %q", rev)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := gitAt(ctx, dir, "diff", "--name-only", "-z", "--no-renames", from, to).Output()
	if err != nil {
		return nil, fmt.Errorf("cannot compare %s and %s", from, to)
	}
	var paths []string
	for _, field := range strings.Split(string(out), "\x00") {
		if field != "" {
			paths = append(paths, field)
		}
	}
	return paths, nil
}

// resolveCommit canonicalizes rev to a full commit object id in the repository
// at dir, or errors. Option-like revisions are refused so untrusted sidecar
// values can never become git flags. Every invocation is argv-only and bounded
// by gitCmdTimeout, so no shell and no unbounded wait.
func resolveCommit(dir, rev string) (string, error) {
	if strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("invalid revision %q", rev)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := gitAt(ctx, dir, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("cannot resolve commit %q in %s", rev, dir)
	}
	sha := strings.TrimSpace(string(out))
	if !fullCommitID.MatchString(sha) {
		return "", fmt.Errorf("cannot resolve commit %q in %s", rev, dir)
	}
	return sha, nil
}

// commitParents returns the full parent ids of commit in dir (nil for a root
// commit). It errors when the commit does not resolve.
func commitParents(dir, commit string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := gitAt(ctx, dir, "rev-list", "--parents", "-n", "1", commit).Output()
	if err != nil {
		return nil, fmt.Errorf("cannot read commit %q in %s", commit, dir)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return nil, fmt.Errorf("cannot read commit %q in %s", commit, dir)
	}
	return fields[1:], nil
}

// isAncestor reports whether ancestor is reachable from descendant in dir.
// An unresolvable object is an error, never a silent "no".
func isAncestor(dir, ancestor, descendant string) error {
	for _, rev := range []string{ancestor, descendant} {
		if strings.HasPrefix(rev, "-") {
			return fmt.Errorf("invalid revision %q", rev)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	cmd := gitAt(ctx, dir, "merge-base", "--is-ancestor", "--", ancestor, descendant)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return fmt.Errorf("%s is not reachable from %s", ancestor, descendant)
		}
		return fmt.Errorf("cannot compare %s and %s in %s", ancestor, descendant, dir)
	}
	return nil
}

// validateMergeReceipt binds a merge receipt to real repository history in the
// repository at dir: the receipt must resolve to a merge commit whose
// second parent is the pinned candidate (or only archive bookkeeping beyond it), whose first parent
// descends from the recorded base commit, and which is reachable from the
// recorded base branch. The returned receipt is the canonical full object id.
func validateMergeReceipt(root, dir string, receipt string, entry integrationrecord.Entry) (string, error) {
	sha, err := resolveCommit(dir, strings.TrimPrefix(receipt, "merge:"))
	if err != nil {
		return "", err
	}
	parents, err := commitParents(dir, sha)
	if err != nil {
		return "", err
	}
	if len(parents) != 2 {
		return "", fmt.Errorf("commit %s is not a two-parent merge commit (use `git merge --no-ff`)", sha)
	}
	if err := integrationCandidateIntact(root, dir, entry, parents[1]); err != nil {
		return "", fmt.Errorf("merge commit %s has an unverified source parent: %w", sha, err)
	}
	if err := isAncestor(dir, entry.BaseCommit, parents[0]); err != nil {
		return "", fmt.Errorf("merge commit %s first parent does not descend from base %s: %w", sha, entry.BaseCommit, err)
	}
	if err := isAncestor(dir, sha, "refs/heads/"+entry.BaseBranch); err != nil {
		return "", fmt.Errorf("merge commit %s is not integrated into branch %q: %w", sha, entry.BaseBranch, err)
	}
	return "merge:" + sha, nil
}

// Only combined-layout archive bookkeeping can follow the pinned candidate.
// Inspect every intervening commit, not just the net diff: edit/revert pairs
// and extra merge parents must not smuggle unverified source into publication.
func integrationCandidateIntact(root, dir string, entry integrationrecord.Entry, candidate string) error {
	if err := isAncestor(dir, entry.SourceCommit, candidate); err != nil {
		return err
	}
	if candidate == entry.SourceCommit {
		return nil
	}
	prefix, combined := recordsGitPrefix(root, dir)
	if !combined {
		return fmt.Errorf("candidate %s is not the recorded source %s", candidate, entry.SourceCommit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := gitAt(ctx, dir, "log", "--format=", "--name-only", "-z", "--no-renames", "--diff-merges=separate", entry.SourceCommit+".."+candidate, "--").Output()
	if err != nil {
		return fmt.Errorf("cannot inspect candidate history: %w", err)
	}
	for _, path := range strings.Split(string(out), "\x00") {
		if path != "" && !isRecordsBookkeeping(prefix, path) {
			return fmt.Errorf("source path %s changed after recorded candidate; re-verify before publication", path)
		}
	}
	return nil
}

// No synthetic merge is needed when the exact candidate is already in the
// receiving history, or a descendant of the pinned base has the identical tree.
func validateUnchangedReceipt(dir, receipt string, entry integrationrecord.Entry) (string, error) {
	sha, err := resolveCommit(dir, strings.TrimPrefix(receipt, "unchanged:"))
	if err != nil {
		return "", err
	}
	if err := isAncestor(dir, entry.BaseCommit, sha); err != nil {
		return "", fmt.Errorf("unchanged receipt does not descend from recorded base: %w", err)
	}
	if err := isAncestor(dir, sha, "refs/heads/"+entry.BaseBranch); err != nil {
		return "", fmt.Errorf("unchanged receipt is not on receiving branch: %w", err)
	}
	// "Already integrated" must have been true at capture. Otherwise this
	// receipt could launder an unverified merge that landed after archival.
	if isAncestor(dir, entry.SourceCommit, entry.BaseCommit) != nil {
		if err := isAncestor(dir, entry.BaseCommit, entry.SourceCommit); err != nil {
			return "", fmt.Errorf("unchanged source does not descend from recorded base: %w", err)
		}
		paths, err := changedPaths(dir, entry.BaseCommit, entry.SourceCommit)
		if err != nil || len(paths) != 0 {
			return "", fmt.Errorf("source is neither already integrated nor tree-identical to recorded base")
		}
	}
	return "unchanged:" + sha, nil
}

// Explicit sidecars must integrate the verified source, not merely some real
// commit in the right object store. This also checks interrupted-close records.
func validateIntegrationSource(root, dir string, st ontostate.State, entry integrationrecord.Entry) error {
	if st.RepoMode != "explicit" {
		return nil
	}
	if !fullCommitID.MatchString(entry.BaseCommit) || !fullCommitID.MatchString(entry.SourceCommit) {
		return fmt.Errorf("repository %s: integration anchors must be canonical commit ids", entry.Alias)
	}
	if err := isAncestor(dir, st.RepoBases[entry.Alias].BaseRef, entry.BaseCommit); err != nil {
		return fmt.Errorf("repository %s: integration base does not descend from immutable diff base: %w", entry.Alias, err)
	}
	head := st.Verify.Heads[entry.Alias]
	if err := isAncestor(dir, head, entry.SourceCommit); err != nil {
		return fmt.Errorf("repository %s: integration source does not contain verified source: %w", entry.Alias, err)
	}
	verified := entry
	verified.SourceCommit = head
	if err := integrationCandidateIntact(root, dir, verified, entry.SourceCommit); err != nil {
		return fmt.Errorf("repository %s: integration source differs from verified source: %w", entry.Alias, err)
	}
	return nil
}
