// Package initws creates and discovers workspace roots before they can be opened.
package initws

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

const (
	controlDir   = ".homonto"
	manifestName = "config.toml"
)

// ErrAlreadyInitialized reports a root that is already a workspace.
var ErrAlreadyInitialized = errors.New("app: this directory is already a Homonto workspace")

// InitInput describes a workspace to create.
type InitInput struct {
	// Root is the workspace root. Empty means the working directory.
	Root string
	// Workflow selects Task or Change. It is a workspace-level choice
	// because the whole integration — which command is installed, which
	// documents exist — follows from it.
	Workflow workspacecfg.Workflow
	// Members are the confirmed member paths. Discovery PROPOSES and a
	// human confirms; nothing is added because a scan found it.
	Members []string
	// Git overrides the git runner; tests inject a recording one.
	Git gitx.Runner
}

// Discovery is what a scan proposes, for a human to confirm.
type Discovery struct {
	Root       string                `json:"root"`
	Candidates []DiscoveredCandidate `json:"candidates"`
}

// DiscoveredCandidate is one proposed member.
type DiscoveredCandidate struct {
	Path string                  `json:"path"`
	Kind workspacecfg.MemberKind `json:"kind"`
	// Manifest is the file that made a non-Git directory a candidate.
	Manifest string `json:"manifest,omitempty"`
}

// Discover proposes members below a root.
//
// It proposes and nothing more: a scan cannot know which directories are
// part of the work, and adding one because it happens to contain a go.mod
// would put a repository under Homonto's control that nobody chose.
func Discover(ctx context.Context, root string, explicit []string, git gitx.Runner) (Discovery, error) {
	resolved, err := resolveRoot(root)
	if err != nil {
		return Discovery{}, err
	}
	scanner := workspace.Scanner{Git: git}
	candidates, err := scanner.Scan(ctx, resolved, workspace.ScanOptions{ExplicitPaths: explicit})
	if err != nil {
		return Discovery{}, err
	}
	out := Discovery{Root: resolved}
	for _, c := range candidates {
		out.Candidates = append(out.Candidates, DiscoveredCandidate{
			Path: c.Path, Kind: c.Kind, Manifest: c.Manifest,
		})
	}
	return out, nil
}

// Init creates a workspace: the control repository, its manifest, and the
// document tree.
//
// It refuses a root that is already a workspace rather than merging into
// one. "Initialize again" is not a thing a workspace can survive: the
// runtime database, the checkpoint, and the manifest describe each other,
// and a second manifest over an existing runtime would describe a
// workspace nobody has.
func Init(ctx context.Context, in InitInput) (workspacecfg.Config, error) {
	root, err := resolveRoot(in.Root)
	if err != nil {
		return workspacecfg.Config{}, err
	}
	switch in.Workflow {
	case workspacecfg.WorkflowTask, workspacecfg.WorkflowChange:
	default:
		return workspacecfg.Config{}, fmt.Errorf("app: workflow %q must be %q or %q",
			in.Workflow, workspacecfg.WorkflowTask, workspacecfg.WorkflowChange)
	}
	manifestPath := filepath.Join(root, controlDir, manifestName)
	if _, err := os.Stat(manifestPath); err == nil {
		return workspacecfg.Config{}, fmt.Errorf("app: %s exists: %w", manifestPath, ErrAlreadyInitialized)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return workspacecfg.Config{}, fmt.Errorf("app: stat %s: %w", manifestPath, err)
	}

	runner := in.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	members, err := confirmMembers(ctx, root, in.Members, runner)
	if err != nil {
		return workspacecfg.Config{}, err
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, fs.ErrNotExist) {
		if _, err := workspace.CreateControlRepository(ctx, root, members, runner); err != nil {
			return workspacecfg.Config{}, err
		}
	}

	cfg, err := buildConfig(root, in.Workflow, members)
	if err != nil {
		return workspacecfg.Config{}, err
	}
	if err := workspacecfg.Validate(root, cfg); err != nil {
		return workspacecfg.Config{}, err
	}
	encoded, err := workspacecfg.Marshal(cfg)
	if err != nil {
		return workspacecfg.Config{}, err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return workspacecfg.Config{}, fmt.Errorf("app: create %s: %w", controlDir, err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return workspacecfg.Config{}, fmt.Errorf("app: write %s: %w", manifestPath, err)
	}
	return cfg, nil
}

// confirmMembers resolves the human-confirmed member paths into candidates.
func confirmMembers(ctx context.Context, root string, paths []string, git gitx.Runner) ([]workspace.Candidate, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	scanner := workspace.Scanner{Git: git}
	candidates, err := scanner.Scan(ctx, root, workspace.ScanOptions{ExplicitPaths: paths})
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, p)
		}
		canon, err := workspace.CanonicalPath(abs)
		if err != nil {
			return nil, fmt.Errorf("app: member %s: %w", p, err)
		}
		wanted[canon] = true
	}
	var out []workspace.Candidate
	for _, c := range candidates {
		if wanted[c.Path] {
			out = append(out, c)
			delete(wanted, c.Path)
		}
	}
	for path := range wanted {
		return nil, fmt.Errorf("app: %s is not a usable member: it is neither a git repository "+
			"nor a directory Homonto recognizes", path)
	}
	return out, nil
}

// buildConfig assembles the manifest for a new workspace.
func buildConfig(root string, workflow workspacecfg.Workflow, members []workspace.Candidate) (workspacecfg.Config, error) {
	workspaceID, err := identity.NewWorkspaceID()
	if err != nil {
		return workspacecfg.Config{}, err
	}
	controlID, err := identity.NewRepositoryID()
	if err != nil {
		return workspacecfg.Config{}, err
	}
	cfg := workspacecfg.Config{
		SchemaVersion: workspacecfg.CurrentSchemaVersion,
		Workspace:     workspacecfg.Workspace{ID: workspaceID, Workflow: workflow},
		Control:       workspacecfg.Control{ID: controlID, Path: "."},
	}
	for _, m := range members {
		rel, err := filepath.Rel(root, m.Path)
		if err != nil {
			return workspacecfg.Config{}, fmt.Errorf("app: member %s: %w", m.Path, err)
		}
		if rel == "." {
			// The control repository is not also a member: it is named by
			// the control block, and listing it twice would give one
			// repository two identities.
			continue
		}
		id, err := identity.NewRepositoryID()
		if err != nil {
			return workspacecfg.Config{}, err
		}
		cfg.Members = append(cfg.Members, workspacecfg.Member{
			ID: id, Path: filepath.ToSlash(rel), Kind: m.Kind,
		})
	}
	return cfg, nil
}

// resolveRoot turns a possibly-empty root into an absolute clean path.
func resolveRoot(root string) (string, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("app: resolve working directory: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("app: resolve %s: %w", root, err)
	}
	return filepath.Clean(abs), nil
}
