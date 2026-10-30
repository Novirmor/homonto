package workspace

import (
	"context"
	"fmt"
	"strings"
)

// PullResult reports a base branch after fetching its upstream.
type PullResult struct {
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
	Before   string `json:"before"`
	After    string `json:"after"`
	// Status is up-to-date, fast-forwarded, or ahead (local commits not on the upstream).
	Status string `json:"status"`
}

// PullBase fetches a local branch's configured upstream and fast-forwards the
// branch to it, so a change can freeze a current base. It never merges,
// rebases, or resets: a diverged branch or a dirty checkout of it is refused.
func PullBase(l Layout, alias, branch string) (PullResult, error) {
	source, err := worktreeAuthority(l, alias)
	if err != nil {
		return PullResult{}, err
	}
	if strings.HasPrefix(branch, "refs/") && !strings.HasPrefix(branch, "refs/heads/") {
		return PullResult{}, fmt.Errorf("pull: --branch %q must name a literal local branch", branch)
	}
	branch = strings.TrimPrefix(branch, "refs/heads/")
	ref := "refs/heads/" + branch
	if branch == "" || strings.HasPrefix(branch, "-") {
		return PullResult{}, fmt.Errorf("pull: invalid branch %q", branch)
	}
	if _, err := pullGit(source, "check-ref-format", ref); err != nil {
		return PullResult{}, fmt.Errorf("pull: invalid branch %q", branch)
	}
	r := PullResult{Repo: alias, Path: source, Branch: branch}
	if r.Before, err = pullGit(source, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}"); err != nil {
		return r, fmt.Errorf("pull: local branch %q does not exist in %s", branch, source)
	}
	remote, _ := pullGit(source, "config", "--get", "branch."+branch+".remote")
	upstream, err := pullGit(source, "rev-parse", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil || remote == "" || remote == "." || !strings.HasPrefix(upstream, "refs/remotes/") {
		return r, fmt.Errorf("pull: branch %q in %s has no remote upstream; set one with `git branch --set-upstream-to=<remote>/<branch> %s`", branch, source, branch)
	}
	r.Upstream = strings.TrimPrefix(upstream, "refs/remotes/")
	if _, err := pullGit(source, "fetch", "--quiet", "--no-tags", "--end-of-options", remote); err != nil {
		return r, fmt.Errorf("pull: fetching %s: %w", remote, err)
	}
	target, err := pullGit(source, "rev-parse", "--verify", "--end-of-options", upstream+"^{commit}")
	if err != nil {
		return r, fmt.Errorf("pull: upstream %s is missing after fetch", r.Upstream)
	}
	r.After = r.Before
	switch {
	case target == r.Before:
		r.Status = "up-to-date"
		return r, nil
	case isAncestorCommit(source, target, r.Before):
		r.Status = "ahead"
		return r, nil
	case !isAncestorCommit(source, r.Before, target):
		return r, fmt.Errorf("pull: branch %q has diverged from %s; reconcile it before starting a change", branch, r.Upstream)
	}
	checkout, err := branchCheckout(source, ref)
	if err != nil {
		return r, err
	}
	if checkout == "" {
		// Git refuses a non-fast-forward or a branch checked out (or being
		// rebased or bisected) in any worktree, so the update stays safe even
		// if another worktree claimed the branch after the probe above.
		if _, err := pullGit(source, "fetch", "--quiet", "--no-tags", ".", upstream+":"+ref); err != nil {
			return r, fmt.Errorf("pull: fast-forwarding %q: %w", branch, err)
		}
	} else {
		dirty, err := pullGit(checkout, "status", "--porcelain=v1", "--untracked-files=no")
		if err != nil {
			return r, err
		}
		if dirty != "" {
			return r, fmt.Errorf("pull: branch %q is checked out with uncommitted changes in %s; it was not updated:\n%s", branch, checkout, dirty)
		}
		if _, err := pullGit(checkout, "merge", "--ff-only", "--quiet", target); err != nil {
			return r, fmt.Errorf("pull: fast-forwarding %q in %s: %w", branch, checkout, err)
		}
	}
	if r.After, err = pullGit(source, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}"); err != nil {
		return r, err
	}
	if r.After != target {
		return r, fmt.Errorf("pull: branch %q is at %s after fast-forward, want %s", branch, r.After, target)
	}
	r.Status = "fast-forwarded"
	return r, nil
}

func pullGit(dir string, args ...string) (string, error) {
	return Git(context.Background(), dir, args...)
}

func isAncestorCommit(dir, ancestor, descendant string) bool {
	_, err := pullGit(dir, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

// branchCheckout returns the worktree that has ref checked out, or "".
func branchCheckout(dir, ref string) (string, error) {
	out, err := pullGit(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for line := range strings.SplitSeq(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch "+ref {
			return path, nil
		}
	}
	return "", nil
}
