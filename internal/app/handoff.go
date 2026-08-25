package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// AttachMapping confirms where one member lives on this machine.
type AttachMapping struct {
	RepositoryID identity.RepositoryID
	Path         string
}

// AttachProposal is one proposed member location, for a human to confirm.
//
// Candidates is a LIST because an ambiguous proposal is the interesting
// case: a member that matched several directories equally is exactly the
// one a human must choose between, and collapsing it to a single path
// would hide the choice being made for them.
type AttachProposal struct {
	RepositoryID identity.RepositoryID `json:"repository_id"`
	Candidates   []string              `json:"candidates,omitempty"`
	Status       string                `json:"status"`
	Reasons      []string              `json:"reasons,omitempty"`
}

// Path returns the single candidate path when a proposal has exactly one,
// and empty otherwise.
func (p AttachProposal) Path() string {
	if len(p.Candidates) == 1 {
		return p.Candidates[0]
	}
	return ""
}

// PreparePortable makes one work's checkpoint transferable.
func (a *App) PreparePortable(ctx context.Context, id identity.WorkID) error {
	return handoff.PreparePortable(ctx, handoff.PortableRequest{
		WorkspaceID: a.cfg.Workspace.ID,
		WorkID:      id,
		ControlRoot: a.controlRoot(),
	})
}

// ProposeAttachMappings suggests where each checkpoint member lives here.
//
// It proposes. Confirming is the human's, because a wrong mapping does not
// fail loudly — it attaches the work to the wrong repository and every
// assignment after that is issued against the wrong tree.
func (a *App) ProposeAttachMappings(ctx context.Context) ([]AttachProposal, error) {
	cp, err := a.checkpoint()
	if err != nil {
		return nil, err
	}
	scanner := workspace.Scanner{}
	candidates, err := scanner.Scan(ctx, a.root, workspace.ScanOptions{})
	if err != nil {
		return nil, err
	}
	proposals := handoff.ProposeMappings(cp, a.cfg, candidates)
	out := make([]AttachProposal, 0, len(proposals))
	for _, p := range proposals {
		proposal := AttachProposal{
			RepositoryID: p.RepositoryID, Status: string(p.Status), Reasons: p.Reasons,
		}
		for _, c := range p.Candidates {
			proposal.Candidates = append(proposal.Candidates, c.Path)
		}
		out = append(out, proposal)
	}
	return out, nil
}

// Attach consumes a transferable checkpoint on this machine.
func (a *App) Attach(ctx context.Context, mappings []AttachMapping, force bool) error {
	confirmed := make([]handoff.ConfirmedMapping, 0, len(mappings))
	for _, m := range mappings {
		confirmed = append(confirmed, handoff.ConfirmedMapping{
			RepositoryID: m.RepositoryID, Path: m.Path,
		})
	}
	stateRoot, err := stateRoot()
	if err != nil {
		return err
	}
	if err := handoff.Attach(ctx, handoff.AttachRequest{
		ControlRoot: a.controlRoot(),
		Mappings:    confirmed,
		Force:       force,
		StateRoot:   stateRoot,
	}); err != nil {
		return err
	}
	return a.resumeAttached(ctx)
}

// resumeAttached gives the attached work a local workflow state.
//
// Attach rebuilds the portable facts — which work, which members, which
// phase — but the workflow's own state machine is not portable and does
// not travel. Without this the work exists in the runtime and no command
// can advance it: `homonto next` reports no active work, on a machine that
// just attached one.
func (a *App) resumeAttached(ctx context.Context) error {
	cp, err := a.checkpoint()
	if err != nil {
		return err
	}
	if cp.Work == nil {
		return nil
	}
	if cp.Work.Workflow == workspacecfg.WorkflowChange {
		_, err := a.changes.Resume(ctx, cp.Work.ID, cp.Work.Name, cp.Work.Phase)
		return err
	}
	_, err = a.engine.Resume(ctx, cp.Work.ID, cp.Work.Name, artifact.Phase(cp.Work.Phase))
	return err
}

// checkpoint reads this workspace's committed checkpoint.
func (a *App) checkpoint() (checkpoint.Checkpoint, error) {
	cp, _, err := checkpoint.Load(handoff.CheckpointPath(a.controlRoot()))
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("app: read the checkpoint: %w", err)
	}
	return cp, nil
}

// stateRoot is this machine's platform state base, where non-Git member
// registrations and leases are slotted.
func stateRoot() (string, error) {
	root, err := registration.StateRoot()
	if err != nil {
		return "", fmt.Errorf("app: resolve the platform state root: %w", err)
	}
	return root, nil
}

// controlRoot is the control repository's absolute path.
func (a *App) controlRoot() string {
	return filepath.Join(a.root, filepath.FromSlash(normalizePath(a.cfg.Control.Path)))
}
