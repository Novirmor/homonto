package ontocli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	return exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-common-dir").Run() == nil
}

// captureVerifyHeads freezes each scoped repository's HEAD (alias "" is the
// config repository) at the moment a verification pass is recorded.
func captureVerifyHeads(root string, st ontostate.State) (map[string]string, error) {
	heads := map[string]string{}
	configHead, err := resolveCommit(root, "HEAD")
	if err != nil {
		return nil, err
	}
	heads[""] = configHead
	names, dirs, err := scopeDirs(root, st.Repos)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		head, err := resolveCommit(dirs[name], "HEAD")
		if err != nil {
			return nil, err
		}
		heads[name] = head
	}
	return heads, nil
}

// workflowBookkeepingPrefixes are the paths a close may legitimately touch
// after the verification pass: the change's own tree, merged living specs,
// numbered ADRs, and updated guides.
var workflowBookkeepingPrefixes = []string{
	"docs/changes/", "docs/specs/", "docs/adr/", "docs/guides/",
}

// verifyHeadsIntact refuses a close whose repositories moved past the frozen
// verification heads in ways the workflow cannot explain: the head must remain
// an ancestor of HEAD, and every path changed since may only be workflow
// bookkeeping. In declared repositories even bookkeeping is unexpected — the
// workflow keeps no tree there — so any change refuses.
func verifyHeadsIntact(root string, st ontostate.State) error {
	names, dirs, err := scopeDirs(root, st.Repos)
	if err != nil {
		return err
	}
	repoDirs := map[string]string{"": root}
	for _, name := range names {
		repoDirs[name] = dirs[name]
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
		changed, err := changedPaths(dir, canonical, "HEAD")
		if err != nil {
			return fmt.Errorf("repository %s: %w", display, err)
		}
		for _, path := range changed {
			if alias != "" {
				return fmt.Errorf("repository %s changed after verification (%s); re-verify the change", display, path)
			}
			if !isWorkflowBookkeeping(path) {
				return fmt.Errorf("source path %s changed after the verification pass; re-verify the change", path)
			}
		}
	}
	return nil
}

func isWorkflowBookkeeping(path string) bool {
	for _, prefix := range workflowBookkeepingPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return strings.HasPrefix(path, ".homonto/")
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
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--name-only", "-z", "--no-renames", from, to).Output()
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
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("cannot resolve commit %q in %s", rev, dir)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("cannot resolve commit %q in %s", rev, dir)
	}
	return sha, nil
}

// commitParents returns the full parent ids of commit in dir (nil for a root
// commit). It errors when the commit does not resolve.
func commitParents(dir, commit string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-list", "--parents", "-n", "1", commit).Output()
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
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", "--", ancestor, descendant)
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
// non-first parents include the recorded source commit, whose first parent
// descends from the recorded base commit, and which is reachable from the
// recorded base branch. The returned receipt is the canonical full object id.
func validateMergeReceipt(dir string, receipt string, entry integrationrecord.Entry) (string, error) {
	sha, err := resolveCommit(dir, strings.TrimPrefix(receipt, "merge:"))
	if err != nil {
		return "", err
	}
	parents, err := commitParents(dir, sha)
	if err != nil {
		return "", err
	}
	if len(parents) < 2 {
		return "", fmt.Errorf("commit %s is not a merge commit (use `git merge --no-ff`)", sha)
	}
	// The recorded source commit must be integrated by the merge: it is an
	// ancestor of one of the merged-in parents (the branch tip may have moved
	// past it after close — e.g. the archive-move commit landed on the branch).
	integrated := false
	for _, parent := range parents[1:] {
		if err := isAncestor(dir, entry.SourceCommit, parent); err == nil {
			integrated = true
			break
		}
	}
	if !integrated {
		return "", fmt.Errorf("merge commit %s does not contain source commit %s", sha, entry.SourceCommit)
	}
	if err := isAncestor(dir, entry.BaseCommit, parents[0]); err != nil {
		return "", fmt.Errorf("merge commit %s first parent does not descend from base %s: %w", sha, entry.BaseCommit, err)
	}
	if err := isAncestor(dir, sha, "refs/heads/"+entry.BaseBranch); err != nil {
		return "", fmt.Errorf("merge commit %s is not integrated into branch %q: %w", sha, entry.BaseBranch, err)
	}
	return "merge:" + sha, nil
}
