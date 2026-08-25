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
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/task"
	"github.com/noviopenworks/homonto/internal/verify"
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

// Integrations returns one integration unit per member that produced
// material. Reviewer and skeptic only ever see the integrated result, so
// there is exactly one area per member for them to look at.
func (e *Environment) Integrations(ctx context.Context, workID identity.WorkID, results []task.Result) ([]task.Partition, error) {
	if len(results) == 0 {
		return nil, nil
	}
	byMember := map[identity.RepositoryID][]task.Result{}
	var order []identity.RepositoryID
	for _, r := range results {
		if _, seen := byMember[r.Partition.Member.ID]; !seen {
			order = append(order, r.Partition.Member.ID)
		}
		byMember[r.Partition.Member.ID] = append(byMember[r.Partition.Member.ID], r)
	}
	var out []task.Partition
	for _, id := range order {
		group := byMember[id]
		member := group[0].Partition.Member
		unit, err := e.integrationFor(ctx, workID, member, group)
		if err != nil {
			return nil, err
		}
		out = append(out, unit)
	}
	return out, nil
}

// integrationFor builds one member's integration area.
func (e *Environment) integrationFor(ctx context.Context, workID identity.WorkID, member task.Member, group []task.Result) (task.Partition, error) {
	scope, err := e.scopeFor(member)
	if err != nil {
		return task.Partition{}, err
	}
	unit := task.Partition{
		Label:       "integrate-" + member.Path,
		Member:      member,
		Integration: true,
		Scope:       scope,
		Prompt: "Combine the parallel implementation results for " + member.Path +
			" into one integrated result and resolve every conflict.",
	}
	if !member.Git {
		if err := e.stageResults(ctx, workID, member, group); err != nil {
			return task.Partition{}, err
		}
		unit.Root = e.relative(e.snapshot.StagePath())
		return unit, nil
	}
	commits := make([]gitx.CommitMaterial, 0, len(group))
	for _, r := range group {
		if r.Material.Kind != protocol.MaterialGitCommit || r.Material.Commit == "" {
			return task.Partition{}, fmt.Errorf(
				"app: action %s produced %q material for the Git member %s; a Git implementer commits its work",
				r.ActionID, r.Material.Kind, member.Path)
		}
		if r.Partition.Base == "" {
			return task.Partition{}, fmt.Errorf(
				"app: action %s recorded no base commit for its isolation area", r.ActionID)
		}
		commits = append(commits, gitx.CommitMaterial{
			RepositoryID: member.ID, ActionID: r.ActionID,
			BaseCommit: r.Partition.Base, Commit: r.Material.Commit,
			ChangedPaths: r.Material.PatchManifest,
		})
	}
	wt, err := e.git.CreateIntegration(ctx, gitx.IntegrationRequest{
		WorkID: workID, RepositoryID: member.ID,
		RepositoryDir: e.memberDir(member), Commits: commits,
	})
	if err != nil {
		return task.Partition{}, err
	}
	// Creating the integration worktree only opens it at the shared base;
	// combining the materials is a separate step, one cherry-pick per
	// material in issue order. A conflict is left IN PLACE — the
	// cherry-pick stays in progress and the paths stay unmerged — because
	// resolving it is exactly what the integration assignment is for.
	var conflicted []string
	for _, m := range commits {
		res, err := e.git.ApplyCommit(ctx, wt, m)
		if err == nil {
			continue
		}
		if len(res.Conflicts) == 0 {
			return task.Partition{}, err
		}
		conflicted = append(conflicted, res.Conflicts...)
	}
	unit.Root = e.relative(wt.Path)
	if len(conflicted) > 0 {
		unit.Prompt += "\n\nA cherry-pick is in progress with conflicts in:\n- " +
			strings.Join(conflicted, "\n- ") +
			"\n\nResolve them, stage the result, and commit."
	}
	return unit, nil
}

// stageResults builds a non-Git member's integration stage by applying
// each implementer's patch in issue order — the snapshot mirror of
// cherry-picking commits onto an integration branch. The last apply runs
// the terminal verification, because no single patch's result digest
// covers a stage that already carries earlier materials.
func (e *Environment) stageResults(ctx context.Context, workID identity.WorkID, member task.Member, group []task.Result) error {
	var prior []snapshot.Assignment
	for i, r := range group {
		if r.Material.Kind != protocol.MaterialSnapshotPatch {
			return fmt.Errorf(
				"app: action %s produced %q material for the non-Git member %s; a non-Git implementer returns a patch manifest",
				r.ActionID, r.Material.Kind, member.Path)
		}
		a := snapshot.Assignment{
			WorkID:       workID,
			ActionID:     r.ActionID,
			RepositoryID: member.ID,
			ManifestPath: snapshot.BaseManifestPath(e.snapshotStore, r.ActionID),
			WorkPath:     filepath.Join(e.root, filepath.FromSlash(r.Partition.Root)),
			BaseDigest:   fingerprint.Digest(r.Partition.Base),
		}
		// The patch file must exist before it can be applied; the final
		// diff gate wrote it when it observed the result, but a recovery
		// that skipped that step recomputes it here rather than failing.
		if _, err := e.snapshot.DiffResult(ctx, a); err != nil {
			return err
		}
		var opts []snapshot.ApplyOption
		if i == len(group)-1 {
			opts = append(opts, snapshot.WithTerminalVerify(prior...))
		}
		if err := e.snapshot.ApplyToStage(ctx, a, opts...); err != nil {
			return err
		}
		prior = append(prior, a)
	}
	// Record what the stage looks like once every material is in it. That
	// is the integrator's starting point, and the only honest thing to
	// hold its result against.
	manifest, err := snapshot.Capture(ctx, e.snapshot.StagePath(), e.snapshotStore, snapshot.CaptureOptions{})
	if err != nil {
		return fmt.Errorf("app: capture the integration stage: %w", err)
	}
	encoded, err := snapshot.EncodeManifest(manifest)
	if err != nil {
		return fmt.Errorf("app: encode the integration stage manifest: %w", err)
	}
	path := e.stageBaselinePath(member.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("app: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("app: write the integration stage manifest: %w", err)
	}
	return nil
}

// stageBaselinePath is where a member's post-integration stage manifest
// lives: the state the integrator started from.
func (e *Environment) stageBaselinePath(repo identity.RepositoryID) string {
	return filepath.Join(e.snapshotStore, "stage-baselines", string(repo)+".json")
}

// SourceFingerprints returns the integrated source fingerprint of every
// member that has an integration area. It is what the checks and the final
// reviews are pinned to, and what a later rebase or amend moves.
func (e *Environment) SourceFingerprints(ctx context.Context, workID identity.WorkID) ([]fingerprint.Digest, error) {
	members, err := e.Members(ctx)
	if err != nil {
		return nil, err
	}
	var out []fingerprint.Digest
	for _, m := range members {
		if !m.Git {
			continue
		}
		dir := gitx.IntegrationPath(e.root, workID, m.ID)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		d, err := e.git.SourceFingerprint(ctx, dir, "HEAD")
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// RunChecks executes every member's configured checks against its
// integrated result, and returns them as one set. Homonto runs them
// itself: an agent's claim that the tests pass is not evidence.
func (e *Environment) RunChecks(ctx context.Context, workID identity.WorkID) (verify.Set, error) {
	members, err := e.Members(ctx)
	if err != nil {
		return verify.Set{}, err
	}
	control, err := e.Control(ctx)
	if err != nil {
		return verify.Set{}, err
	}
	sources, err := e.SourceFingerprints(ctx, workID)
	if err != nil {
		return verify.Set{}, err
	}
	combined := verify.Set{Inputs: verify.Inputs{Repository: control.ID, Sources: sources}}
	base, err := e.Fingerprints(ctx)
	if err != nil {
		return verify.Set{}, err
	}
	combined.Inputs.Config = base.CheckConfig
	for _, m := range members {
		if !hasMember(e.cfg, m.ID) {
			continue
		}
		specs, err := verify.SpecsFor(e.cfg, m.ID)
		if err != nil {
			return verify.Set{}, err
		}
		if len(specs) == 0 {
			continue
		}
		dir := e.checkDir(workID, m)
		runner, err := verify.NewRunner(dir, verify.Options{Lookup: e.lookup})
		if err != nil {
			return verify.Set{}, err
		}
		set, err := runner.Run(ctx, combined.Inputs, specs)
		if err != nil {
			return verify.Set{}, err
		}
		combined.Results = append(combined.Results, set.Results...)
		if combined.At.IsZero() {
			combined.At = set.At
		}
	}
	return combined, nil
}

// checkDir is where a member's checks run: its integration area when one
// exists, its own root otherwise.
func (e *Environment) checkDir(workID identity.WorkID, m task.Member) string {
	if m.Git {
		dir := gitx.IntegrationPath(e.root, workID, m.ID)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		return e.memberDir(m)
	}
	stage := e.snapshot.StagePath()
	if _, err := os.Stat(stage); err == nil {
		return stage
	}
	return e.memberDir(m)
}

// ResultDiff observes what an assignment actually changed. It is read from
// the isolation area itself — a Git worktree's committed diff or a
// snapshot's captured patch — never from the report, which is exactly why
// the final-diff gate catches what the write hook missed.
func (e *Environment) ResultDiff(ctx context.Context, action protocol.Action, unit task.Partition) (guard.ResultDiff, error) {
	diff := guard.ResultDiff{Root: action.WorkingDirectory}
	abs := filepath.Join(e.root, filepath.FromSlash(action.WorkingDirectory))
	if _, err := os.Stat(abs); err != nil {
		return diff, fmt.Errorf("app: isolation area %s: %w", action.WorkingDirectory, err)
	}
	// Which observer to use is decided by the action's MEMBER, never by
	// probing the directory: a non-Git isolation area lives under the
	// control repository's own tree, so `git rev-parse` would happily
	// claim it as a worktree and report the control repository's changes
	// as the assignment's.
	if !e.memberIsGit(action.Repository.ID) {
		return e.snapshotDiff(ctx, action, unit, diff)
	}
	out, err := e.runner.Run(ctx, abs, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return guard.ResultDiff{}, err
	}
	uncommitted := parsePorcelain(out)
	committed, err := e.committedPaths(ctx, abs)
	if err != nil {
		return guard.ResultDiff{}, err
	}
	diff.Changes = append(uncommitted, committed...)
	return diff, nil
}

// snapshotDiff observes a non-Git isolation area by comparing it against
// the captured base manifest.
func (e *Environment) snapshotDiff(ctx context.Context, action protocol.Action, unit task.Partition, diff guard.ResultDiff) (guard.ResultDiff, error) {
	// An implementer is compared against the snapshot its isolation area
	// was materialized from. An INTEGRATOR is compared against the stage
	// as Homonto built it: the combined result is the integrator's
	// starting point, and holding it against an implementer's base would
	// report every other implementer's work as the integrator's.
	manifestPath := snapshot.BaseManifestPath(e.snapshotStore, action.ID)
	if unit.Integration {
		manifestPath = e.stageBaselinePath(action.Repository.ID)
	}
	patch, err := e.snapshot.DiffResult(ctx, snapshot.Assignment{
		ActionID:     action.ID,
		RepositoryID: action.Repository.ID,
		ManifestPath: manifestPath,
		WorkPath:     filepath.Join(e.root, filepath.FromSlash(action.WorkingDirectory)),
	})
	if err != nil {
		return guard.ResultDiff{}, err
	}
	for _, op := range patch.Operations {
		kind := guard.ChangeModified
		switch op.Op {
		case snapshot.OpAdd:
			kind = guard.ChangeAdded
		case snapshot.OpDelete:
			kind = guard.ChangeDeleted
		}
		diff.Changes = append(diff.Changes, guard.Change{Path: op.Path, Kind: kind})
		if op.OldPath != "" {
			diff.Changes = append(diff.Changes, guard.Change{Path: op.OldPath, Kind: guard.ChangeDeleted})
		}
	}
	return diff, nil
}

// committedPaths lists the paths a worktree committed on top of its base.
func (e *Environment) committedPaths(ctx context.Context, dir string) ([]guard.Change, error) {
	out, err := e.runner.Run(ctx, dir, "diff", "--name-status", "-z", "HEAD@{upstream}...HEAD")
	if err != nil {
		// No upstream: compare against the merge base with the default
		// branch is not knowable here, so fall back to the last commit.
		out, err = e.runner.Run(ctx, dir, "diff", "--name-status", "-z", "HEAD~1..HEAD")
		if err != nil {
			return nil, nil
		}
	}
	return parseNameStatus(out), nil
}

// memberIsGit reports whether a member is Git-backed, per the manifest.
// The control repository is always Git-backed.
func (e *Environment) memberIsGit(repo identity.RepositoryID) bool {
	for _, m := range e.cfg.Members {
		if m.ID == repo {
			return m.Kind != workspacecfg.KindNonGit
		}
	}
	return repo == e.cfg.Control.ID
}

// parsePorcelain reads `git status --porcelain=v1 -z` into changes.
func parsePorcelain(out string) []guard.Change {
	var changes []guard.Change
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		p := strings.TrimSpace(entry[3:])
		if p == "" {
			continue
		}
		kind := guard.ChangeModified
		switch {
		case strings.Contains(code, "?") || strings.Contains(code, "A"):
			kind = guard.ChangeAdded
		case strings.Contains(code, "D"):
			kind = guard.ChangeDeleted
		}
		changes = append(changes, guard.Change{Path: p, Kind: kind})
	}
	return changes
}

// parseNameStatus reads `git diff --name-status -z` into changes.
func parseNameStatus(out string) []guard.Change {
	fields := strings.Split(out, "\x00")
	var changes []guard.Change
	for i := 0; i+1 < len(fields); i += 2 {
		status, p := fields[i], fields[i+1]
		if status == "" || p == "" {
			continue
		}
		kind := guard.ChangeModified
		switch status[0] {
		case 'A':
			kind = guard.ChangeAdded
		case 'D':
			kind = guard.ChangeDeleted
		}
		changes = append(changes, guard.Change{Path: p, Kind: kind})
	}
	return changes
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
