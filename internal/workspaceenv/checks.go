// Checks and observed evidence: source digests, check runs, and result diffs.
package workspaceenv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// SourceDigest digests a non-Git member's current content.
//
// A non-Git member has no commit to anchor to, so its content IS its
// anchor: the checkpoint records what the directory digested to, and
// another machine compares that against what it finds. Capture is
// content-addressed, so digesting a directory twice writes nothing the
// second time.
func (e *Environment) SourceDigest(ctx context.Context, dir string) (fingerprint.Digest, error) {
	manifest, err := snapshot.Capture(ctx, dir, e.snapshotStore, snapshot.CaptureOptions{})
	if err != nil {
		return "", fmt.Errorf("app: digest %s: %w", dir, err)
	}
	return snapshot.DigestManifest(manifest), nil
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
