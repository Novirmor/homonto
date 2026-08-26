package workspaceenv

import (
	"context"
	"os"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
)

// WorkspaceDiff returns what changed across the workspace since a work baseline.
func (e *Environment) WorkspaceDiff(ctx context.Context, workID identity.WorkID, _ []fingerprint.Digest) ([]pathclass.DiffEntry, error) {
	members, err := e.Members(ctx)
	if err != nil {
		return nil, err
	}
	var out []pathclass.DiffEntry
	for _, member := range members {
		if !member.Git {
			continue
		}
		dir := gitx.IntegrationPath(e.root, workID, member.ID)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		raw, err := e.runner.Run(ctx, dir, "diff", "--name-status", "-z", "HEAD@{upstream}...HEAD")
		if err != nil {
			continue
		}
		for _, change := range parseNameStatus(raw) {
			out = append(out, pathclass.DiffEntry{Member: member.Path, Path: change.Path, Op: toPathClassOp(change.Kind)})
		}
	}
	return out, nil
}

// Matchers resolves a member's path-class matcher by its workspace-relative path.
func (e *Environment) Matchers(member string) (*pathclass.Matcher, error) {
	for _, configured := range e.cfg.Members {
		if normalizePath(configured.Path) == normalizePath(member) {
			return pathclass.NewMatcher(configured.Paths)
		}
	}
	return pathclass.NewMatcher(nil)
}

func toPathClassOp(kind guard.ChangeKind) pathclass.Op {
	switch kind {
	case guard.ChangeAdded:
		return pathclass.OpAdded
	case guard.ChangeDeleted:
		return pathclass.OpDeleted
	}
	return pathclass.OpModified
}
