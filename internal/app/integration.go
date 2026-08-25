// Integration areas: combining parallel results into one per member.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/task"
)

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
