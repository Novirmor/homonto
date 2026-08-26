// Package app is the composition root of the rewritten workflow: it opens
// a workspace, wires every service the engines need, and implements the
// workspace-shaped facts the engines deliberately do not know how to
// compute for themselves.
//
// Nothing here decides workflow policy. The Task engine sequences and
// gates; this package answers its questions about the repository on disk —
// who the members are, what the current fingerprints are, where an
// isolation area goes, what the checks actually printed.
package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/task"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// excludedFromScope are the directories no assignment scope ever includes.
// Both are Homonto's or Git's own state, and an assignment that could
// write either could rewrite the record of its own work.
var excludedFromScope = map[string]bool{
	".git":     true,
	".homonto": true,
	".jj":      true,
}

// Environment implements task.Environment over a real workspace.
type Environment struct {
	root          string
	cfg           workspacecfg.Config
	git           *gitx.Service
	runner        gitx.Runner
	snapshot      *snapshot.Service
	snapshotStore string
	lookup        func(string) (string, bool)
}

// Environment serves both workflow engines without an adapter.
var _ task.Environment = (*Environment)(nil)

// NewEnvironment binds an environment to a validated workspace.
// snapshotStore is the non-Git snapshot store root the snapshot service
// was opened on; the environment needs it to locate a captured base
// manifest when it observes a non-Git result.
func NewEnvironment(root string, cfg workspacecfg.Config, git *gitx.Service, runner gitx.Runner, snap *snapshot.Service, snapshotStore string) (*Environment, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("app: workspace root %q must be an absolute path", root)
	}
	if git == nil || snap == nil {
		return nil, fmt.Errorf("app: the environment needs both the git and snapshot services")
	}
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	return &Environment{
		root: root, cfg: cfg, git: git, runner: runner,
		snapshot: snap, snapshotStore: snapshotStore, lookup: os.LookupEnv,
	}, nil
}

// Control returns the control repository member.
func (e *Environment) Control(context.Context) (task.Member, error) {
	return task.Member{
		ID:   e.cfg.Control.ID,
		Path: normalizePath(e.cfg.Control.Path),
		Git:  true,
	}, nil
}

// Members returns every confirmed repository. The control repository is
// always a member: a task that does not survey the repository holding its
// own record has not surveyed the workspace.
func (e *Environment) Members(ctx context.Context) ([]task.Member, error) {
	control, err := e.Control(ctx)
	if err != nil {
		return nil, err
	}
	out := []task.Member{control}
	seen := map[identity.RepositoryID]bool{control.ID: true}
	for _, m := range e.cfg.Members {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, task.Member{
			ID:   m.ID,
			Path: normalizePath(m.Path),
			Git:  m.Kind != workspacecfg.KindNonGit,
		})
	}
	return out, nil
}

// Fingerprints returns the membership, path-class, and check-configuration
// digests the workflow's baseline pins. The per-member path-class and
// verification digests are folded into one digest each, in member order,
// so a change to any member moves the workspace-wide value.
func (e *Environment) Fingerprints(ctx context.Context) (task.Baseline, error) {
	members, err := e.Members(ctx)
	if err != nil {
		return task.Baseline{}, err
	}
	var pathParts, checkParts []string
	for _, m := range members {
		if m.ID == e.cfg.Control.ID && !hasMember(e.cfg, m.ID) {
			// The control repository need not be a configured member; when
			// it is not, it declares no path classes or checks of its own.
			continue
		}
		pc, err := workspacecfg.PathClassFingerprint(e.cfg, m.ID)
		if err != nil {
			return task.Baseline{}, err
		}
		vc, err := workspacecfg.VerificationFingerprint(e.cfg, m.ID)
		if err != nil {
			return task.Baseline{}, err
		}
		pathParts = append(pathParts, string(m.ID)+"="+string(pc))
		checkParts = append(checkParts, string(m.ID)+"="+string(vc))
	}
	return task.Baseline{
		Membership:  workspacecfg.MembershipFingerprint(e.cfg),
		PathClass:   fingerprint.Bytes("workspace-path-classes", []byte(strings.Join(pathParts, "\n"))),
		CheckConfig: fingerprint.Bytes("workspace-check-config", []byte(strings.Join(checkParts, "\n"))),
	}, nil
}

// hasMember reports whether repo is in the configured member list.
func hasMember(cfg workspacecfg.Config, repo identity.RepositoryID) bool {
	for _, m := range cfg.Members {
		if m.ID == repo {
			return true
		}
	}
	return false
}

// Partition splits the open checklist items into parallel units: one per
// item per WORK member. Maximum parallelism is deliberate — units that may
// later conflict still run side by side, because the integration
// assignment exists precisely to resolve that.
//
// The work members are every confirmed member except the control
// repository, when there are any. The control repository holds the record
// rather than the code, and issuing implementation work into it would put
// an assignment's isolation area in the same tree Homonto is writing the
// task document into. A workspace whose ONLY member is the control
// repository is the exception: there, the control repository is also where
// the code lives.
//
// The isolation area is left empty — Isolate creates it once the action id
// exists, because a worktree is named after the action it serves.
func (e *Environment) Partition(ctx context.Context, _ identity.WorkID, items []artifact.Item) ([]task.Partition, error) {
	if len(items) == 0 {
		return nil, nil
	}
	members, err := e.workMembers(ctx)
	if err != nil {
		return nil, err
	}
	var out []task.Partition
	for _, it := range items {
		for _, m := range members {
			scope, err := e.scopeFor(m)
			if err != nil {
				return nil, err
			}
			out = append(out, task.Partition{
				Label:  fmt.Sprintf("item-%d-%s", it.Index, m.Path),
				Member: m,
				Items:  []int{it.Index},
				Scope:  scope,
				Prompt: "In " + m.Path + ", implement this checklist item and nothing else:\n\n- " + it.Text,
			})
		}
	}
	return out, nil
}

// workMembers returns the members implementation work is issued into.
func (e *Environment) workMembers(ctx context.Context) ([]task.Member, error) {
	members, err := e.Members(ctx)
	if err != nil {
		return nil, err
	}
	var work []task.Member
	for _, m := range members {
		if m.ID == e.cfg.Control.ID {
			continue
		}
		work = append(work, m)
	}
	if len(work) == 0 {
		control, err := e.Control(ctx)
		if err != nil {
			return nil, err
		}
		return []task.Member{control}, nil
	}
	return work, nil
}

// Isolate creates the isolation area for one action: a Git worktree for a
// Git member, a content-hashed snapshot for a non-Git one. Both are
// separate working trees — the member's own tree is never touched.
func (e *Environment) Isolate(ctx context.Context, workID identity.WorkID, actionID identity.ActionID, unit task.Partition) (task.Partition, error) {
	dir := e.memberDir(unit.Member)
	if unit.Member.Git {
		wt, err := e.git.CreateAssignment(ctx, gitx.CreateRequest{
			WorkID: workID, ActionID: actionID,
			RepositoryID: unit.Member.ID, RepositoryDir: dir,
			BaseCommit: "HEAD", Scope: unit.Scope,
		})
		if err != nil {
			return task.Partition{}, err
		}
		unit.Root = e.relative(wt.Path)
		unit.Base = wt.BaseCommit
		return unit, nil
	}
	a, err := e.snapshot.CreateAssignment(ctx, snapshot.AssignmentRequest{
		WorkID: workID, ActionID: actionID,
		RepositoryID: unit.Member.ID, SourceDir: dir,
		Exclusions: e.excludedGlobs(unit.Member.ID),
	})
	if err != nil {
		return task.Partition{}, err
	}
	unit.Root = e.relative(a.WorkPath)
	unit.Base = string(a.BaseDigest)
	return unit, nil
}

// scopeFor lists the top-level directories an assignment in a member may
// write. It is explicit on purpose: an empty scope reads as "unrestricted"
// to the guard, and an unrestricted assignment has no boundary at all.
// Git's and Homonto's own state are excluded, and so is anything the
// member classifies as vendored or generated.
func (e *Environment) scopeFor(m task.Member) ([]string, error) {
	dir := e.memberDir(m)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("app: list %s: %w", m.Path, err)
	}
	excluded := e.excludedGlobs(m.ID)
	var scope []string
	for _, entry := range entries {
		name := entry.Name()
		if excludedFromScope[name] || matchesAny(excluded, name) {
			continue
		}
		scope = append(scope, name)
	}
	if len(scope) == 0 {
		return nil, fmt.Errorf(
			"app: member %s offers nothing an assignment may write; every entry is excluded", m.Path)
	}
	sort.Strings(scope)
	return scope, nil
}

// excludedGlobs returns a member's vendored and generated path patterns.
func (e *Environment) excludedGlobs(repo identity.RepositoryID) []string {
	for _, m := range e.cfg.Members {
		if m.ID != repo || m.Paths == nil {
			continue
		}
		return append(append([]string(nil), m.Paths.Vendored...), m.Paths.Generated...)
	}
	return nil
}

// matchesAny reports whether name matches any glob, comparing the pattern's
// first segment so "vendor/**" excludes the "vendor" directory.
func matchesAny(globs []string, name string) bool {
	for _, g := range globs {
		head, _, _ := strings.Cut(g, "/")
		if ok, err := path.Match(head, name); err == nil && ok {
			return true
		}
	}
	return false
}

// memberDir is a member's absolute directory.
func (e *Environment) memberDir(m task.Member) string {
	if m.Path == "." || m.Path == "" {
		return e.root
	}
	return filepath.Join(e.root, filepath.FromSlash(m.Path))
}

// relative expresses an absolute path inside the workspace as a clean
// workspace-relative slash path.
func (e *Environment) relative(abs string) string {
	rel, err := filepath.Rel(e.root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// normalizePath returns a configured member path in the workspace-relative
// slash form the protocol uses.
func normalizePath(p string) string {
	if p == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(p))
}
