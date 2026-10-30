package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeRemoval is a read-only removal assessment, not an execution binding.
// It deliberately omits the private ownership token.
type WorktreeRemoval struct {
	Repo       string `json:"repo"`
	Role       string `json:"role"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	StateID    string `json:"state_id"`
	Head       string `json:"head,omitempty"`
	Target     string `json:"target,omitempty"`
	Recovering bool   `json:"recovering,omitempty"`
	Blocker    string `json:"blocker,omitempty"`
}

// InspectWorkflowRemovals reports all bindings reserving this name. An invalid
// binding remains visible as a blocker; it is never silently dropped or adopted.
// record is an absolute, already-validated native record directory.
func InspectWorkflowRemovals(l Layout, workflow, change, record string) ([]WorktreeRemoval, error) {
	if err := checkWorktreeNames(workflow, change); err != nil {
		return nil, err
	}
	s, err := readWorktreeState(record, workflow)
	if err != nil {
		return nil, err
	}
	if s.Change != change {
		return nil, fmt.Errorf("cleanup: record name changed")
	}
	r, err := readWorktreeRegistry(l)
	if err != nil {
		return nil, err
	}
	result := []WorktreeRemoval{}
	for _, w := range r.Entries {
		if w.Workflow != workflow || w.Change != change {
			continue
		}
		if w.StateID != s.identity && filepath.Dir(record) == filepath.Join(worktreeStatesDir(l, workflow), "archive") {
			// A later generation can legitimately own this name after the old
			// one was cleaned. Never make old archives adopt its bindings.
			continue
		}
		role := w.Role
		if role == "" {
			role = "execution"
		}
		p := WorktreeRemoval{Repo: w.Repo, Role: role, Path: w.Path, Branch: w.Branch, StateID: w.StateID, Recovering: w.Status == "removing"}
		if w.StateID != s.identity {
			p.Blocker = "binding belongs to another generation; inspect before recovery"
		} else {
			p.Head, p.Target, _, err = checkWorktreeRemoval(l, w)
			if err != nil {
				p.Blocker = err.Error()
			}
		}
		result = append(result, p)
	}
	return result, nil
}

func checkWorktreeRemoval(l Layout, w Worktree) (head, target string, removed bool, err error) {
	if w.Status == "removing" {
		if _, statErr := os.Lstat(w.Path); os.IsNotExist(statErr) {
			err = checkRemovedWorktree(l, w)
			return "", "", true, err
		} else if statErr != nil {
			return "", "", false, statErr
		}
		// A crash before Git removal left the original checkout in place. All
		// ownership, cleanliness and integration checks still apply on retry.
		w.Status = "ready"
	}
	if err = validateWorktree(l, w); err != nil {
		return
	}
	s, stateErr := bindingState(l, w.Workflow, w.Change, w.StateID, false)
	if stateErr != nil {
		err = stateErr
		return
	}
	if !s.terminal(w.Workflow) {
		err = fmt.Errorf("worktree: active binding cannot be removed; complete or explicitly abandon %s/%s first", w.Workflow, w.Change)
		return
	}
	if err = cleanWorktree(w.Path); err != nil {
		return
	}
	head, err = worktreeGit(w.Path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return
	}
	target, err = worktreeGit(w.Path, "rev-parse", "--verify", "--end-of-options", w.BaseTarget+"^{commit}")
	if err != nil {
		return
	}
	if _, err = worktreeGit(w.Path, "merge-base", "--is-ancestor", head, target); err == nil {
		return
	}
	// A closed change delivered through a PR is not in the base yet. Its commits
	// stay safe on the retained execution branch, which removal never deletes
	// (ADR 0086), so a HEAD that branch contains may go.
	if w.Branch != "" {
		if _, branchErr := worktreeGit(w.Path, "merge-base", "--is-ancestor", head, "refs/heads/"+w.Branch); branchErr == nil {
			err = nil
			return
		}
	}
	err = fmt.Errorf("worktree: source commit %s is neither integrated into %q nor on the retained branch %q; refusing removal", head, w.BaseRef, w.Branch)
	return
}

// Only a recorded removal intent can release an already-missing checkout. Never
// prune arbitrary missing registrations: both Git's admin directory and live
// worktree list must confirm that the previously authorized removal completed.
func checkRemovedWorktree(l Layout, w Worktree) error {
	if w.WorkflowRoot != l.WorkflowRoot {
		return fmt.Errorf("worktree: removal workflow root changed")
	}
	parent, err := worktreeParent(l)
	if err != nil {
		return err
	}
	repoDir := w.Repo
	if repoDir == "" {
		repoDir = "_config"
	}
	expected := filepath.Join(parent, repoDir, w.Workflow+"-"+w.Change)
	if w.Role == "receiver" {
		expected = filepath.Join(parent, repoDir, ".receivers", w.Workflow+"-"+w.Change)
	}
	if w.Parent != parent || w.Path != expected {
		return fmt.Errorf("worktree: removal path is outside its recorded ownership")
	}
	if w.Ownership == "" || w.GitDir == "" || filepath.Dir(w.GitDir) != filepath.Join(w.CommonDir, "worktrees") {
		return fmt.Errorf("worktree: incomplete removal identity")
	}
	authority, err := worktreeAuthority(l, w.Repo)
	if err != nil {
		return err
	}
	common, err := worktreeCommon(authority)
	if err != nil || common != w.CommonDir {
		return fmt.Errorf("worktree: removal authority changed: %v", err)
	}
	for _, path := range []string{w.Path, w.GitDir} {
		if err := realWorktreePath(path, true); err != nil {
			return err
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("worktree: removal at %s is incomplete; inspect before recovery", path)
		}
	}
	listed, err := worktreeGit(authority, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	for _, field := range strings.Split(listed, "\x00") {
		if field == "worktree "+w.Path {
			return fmt.Errorf("worktree: removed path is still registered: %s", w.Path)
		}
	}
	s, err := bindingState(l, w.Workflow, w.Change, w.StateID, false)
	if err != nil {
		return err
	}
	if !s.terminal(w.Workflow) {
		return fmt.Errorf("worktree: removal generation is not terminal")
	}
	return nil
}
