package handoff

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// ConfirmedMapping is one human-confirmed member mapping: the checkpoint
// member's repository id and the absolute path of the member root on this
// machine.
type ConfirmedMapping struct {
	RepositoryID identity.RepositoryID
	Path         string
}

// AttachRequest names the cloned control repository being attached, the
// confirmed member mappings, and the takeover flag.
type AttachRequest struct {
	// ControlRoot is the canonical absolute path of the cloned control
	// repository carrying the transferable checkpoint.
	ControlRoot string
	// Mappings confirm every non-control checkpoint member's location on
	// this machine (ProposeMappings proposes; the human confirms).
	Mappings []ConfirmedMapping
	// Force is the human-confirmed takeover of an already-consumed
	// checkpoint: generation increment, forced_takeover decision, all
	// evidence marked stale. On an already-transferable checkpoint force
	// is a normal attach — the takeover bump applies only to consumed.
	Force bool
	// StateRoot is this machine's platform state base — where non-git
	// member registrations and leases are slotted (a fresh HOME works; the
	// old machine's slots simply are not here).
	StateRoot string
	// Git inspects git members; nil means gitx.ExecRunner{}.
	Git gitx.Runner
}

// journaledLeaseTarget is one lease target in the attach payload. It
// deliberately mirrors the lease package's acquisition payload shape
// (repository_id/path/token JSON names): the operation journal IS the
// recorded token store for whichever operation the sentinel names —
// including this composed attach.
type journaledLeaseTarget struct {
	RepositoryID identity.RepositoryID `json:"repository_id"`
	Path         string                `json:"path"`
	Token        identity.Token        `json:"token"`
}

// attachPayload is the journaled parameters of one attach.
type attachPayload struct {
	WorkspaceID identity.WorkspaceID `json:"workspace_id"`
	WorkID      identity.WorkID      `json:"work_id"`
	Generation  uint64               `json:"generation"`
	ControlRoot string               `json:"control_root"`
	TransferID  identity.Token       `json:"transfer_id"`
	Force       bool                 `json:"force"`
	Mappings    []ConfirmedMapping   `json:"mappings"`
	// Targets is the lease token store of this attach's composed
	// acquisition (see journaledLeaseTarget).
	Targets []journaledLeaseTarget `json:"targets"`
}

// attachOperation is the journaled all-or-none attach.
type attachOperation struct {
	id         identity.OperationID
	workID     identity.WorkID
	generation uint64
	payload    attachPayload
	effects    []operation.Effect
}

func (o *attachOperation) ID() identity.OperationID    { return o.id }
func (o *attachOperation) Kind() string                { return "handoff.attach" }
func (o *attachOperation) WorkID() identity.WorkID     { return o.workID }
func (o *attachOperation) Generation() int64           { return int64(o.generation) }
func (o *attachOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *attachOperation) Payload() any                { return o.payload }
func (o *attachOperation) Effects() []operation.Effect { return o.effects }

// Attach consumes a transferable checkpoint on this machine: claims every
// checkpoint member's registration, acquires the full lease set at the
// checkpoint generation, rebuilds the runtime database from portable
// inputs, marks the checkpoint consumed (same generation, same transfer
// id), and commits the result to the control repository. See the package
// doc for the force-takeover, rollback, and crash semantics.
func Attach(ctx context.Context, req AttachRequest) error {
	root, err := workspace.CanonicalPath(req.ControlRoot)
	if err != nil {
		return err
	}
	if err := validateStateRoot(req.StateRoot); err != nil {
		return err
	}
	runner := req.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}

	cp, cfg, err := loadCheckpointState(ctx, root)
	if err != nil {
		return err
	}
	if err := checkAttachable(cp, req.Force); err != nil {
		return err
	}

	db, err := openRuntime(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	// A crashed earlier attach converges before this one journals anything:
	// completed means the checkpoint below now reads consumed.
	if err := Recover(ctx, db); err != nil {
		return fmt.Errorf("handoff: attach: recover pending operations: %w", err)
	}
	cp, cfg, err = loadCheckpointState(ctx, root)
	if err != nil {
		return err
	}
	if err := checkAttachable(cp, req.Force); err != nil {
		return err
	}
	if cp.Work == nil {
		return fmt.Errorf("handoff: attach: checkpoint carries no active work: %w", ErrNoActiveWork)
	}

	memberRoots, err := confirmMappings(ctx, cp, cfg, root, req.Mappings, runner)
	if err != nil {
		return err
	}

	var effects []operation.Effect
	cp, takeoverWrite, forceTakeover, err := takeoverCheckpoint(cp, cfg, root, req.Force)
	if err != nil {
		return err
	}
	if takeoverWrite != nil {
		effects = append(effects, takeoverWrite)
	}
	// takeoverCheckpoint returns cp already bumped, so the handoff
	// generation it carries is the effective one from here on.
	effectiveGen := cp.Handoff.Generation

	prov, err := lease.CurrentProcess()
	if err != nil {
		return err
	}
	site := attachSite{runner: runner, stateRoot: req.StateRoot, root: root, memberRoots: memberRoots}
	claims, leases, leaseList, targets, err := planMemberClaims(ctx, cp, cfg, site, prov)
	if err != nil {
		return err
	}

	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("handoff: attach: operation id: %w", err)
	}
	sentinel := lease.SentinelContent{
		SchemaVersion: 1,
		WorkspaceID:   cp.WorkspaceID,
		WorkID:        cp.Work.ID,
		Generation:    effectiveGen,
		Version:       1,
		OperationID:   opID,
		Leases:        leaseList,
	}
	if err := sentinel.Validate(); err != nil {
		return fmt.Errorf("handoff: attach: sentinel: %w", err)
	}

	rebuild, err := buildRebuildPayload(cfg, cp, memberRootsForRebuild(cp, cfg, memberRoots, root), forceTakeover)
	if err != nil {
		return fmt.Errorf("handoff: attach: %w", err)
	}

	consumed := cp
	consumed.Handoff = checkpoint.Handoff{
		State:      checkpoint.HandoffConsumed,
		Generation: effectiveGen,
		TransferID: cp.Handoff.TransferID,
	}
	if err := checkpoint.ValidateTransition(cp, consumed); err != nil {
		return fmt.Errorf("handoff: attach: consume: %w", err)
	}

	// Journal order is load-bearing: recovery replays effects by
	// sequence, so the takeover write leads and the consume write lands
	// only after everything it depends on.
	effects = append(effects, claims...)
	effects = append(effects, leases...)
	effects = append(effects, &sentinelWriteEffect{payload: sentinelPayload{
		Path: lease.SentinelPath(root, cp.Work.ID), Content: sentinel,
	}})
	effects = append(effects, &rebuildEffect{payload: rebuild, db: db})
	effects = append(effects, &checkpointWriteEffect{payload: checkpointWritePayload{
		Path: CheckpointPath(root), Next: consumed, Prev: cp,
	}})
	effects = append(effects, &commitEffect{payload: commitPayload{
		Root:    root,
		Message: fmt.Sprintf("homonto: attach %d", effectiveGen),
	}})

	op := &attachOperation{
		id: opID, workID: cp.Work.ID, generation: effectiveGen,
		payload: attachPayload{
			WorkspaceID: cp.WorkspaceID, WorkID: cp.Work.ID,
			Generation: effectiveGen, ControlRoot: root,
			TransferID: consumed.Handoff.TransferID, Force: forceTakeover,
			Mappings: req.Mappings,
			Targets:  targets,
		},
		effects: effects,
	}
	if err := runOperation(ctx, db, op); err != nil {
		return fmt.Errorf("handoff: attach %s: %w", opID, err)
	}
	return nil
}

// attachSite is the machine-local side of an attach: how git runs here,
// where the control repository and each confirmed member live, and where
// non-git state is slotted. It exists so planMemberClaims takes one site
// instead of four loose inputs.
type attachSite struct {
	runner      gitx.Runner
	stateRoot   string
	root        string
	memberRoots map[identity.RepositoryID]string
}

// takeoverCheckpoint force-reopens a consumed checkpoint. Consumed has no
// legal forward transition, so the takeover re-marks it transferable at
// generation+1 with a fresh transfer id (ValidateTransition demands the
// bump); the attach above then consumes at that generation. A transferable
// checkpoint needs no takeover: force is the remedy for consumption
// elsewhere, not a mode flag — in that case cp comes back unchanged with a
// nil effect.
func takeoverCheckpoint(cp checkpoint.Checkpoint, cfg workspacecfg.Config, root string, force bool) (checkpoint.Checkpoint, operation.Effect, bool, error) {
	if !(force && cp.Handoff.State == checkpoint.HandoffConsumed) {
		return cp, nil, false, nil
	}
	takeover := cp
	takeover.Handoff = checkpoint.Handoff{
		State:      checkpoint.HandoffTransferable,
		Generation: cp.Handoff.Generation + 1,
		TransferID: mustNewToken(),
	}
	if err := checkpoint.ValidateTransition(cp, takeover); err != nil {
		return checkpoint.Checkpoint{}, nil, false, fmt.Errorf("handoff: attach: force takeover: %w", err)
	}
	if err := checkpoint.Validate(takeover, cfg); err != nil {
		return checkpoint.Checkpoint{}, nil, false, fmt.Errorf("handoff: attach: force takeover: %w", err)
	}
	return takeover, &checkpointWriteEffect{payload: checkpointWritePayload{
		Path: CheckpointPath(root), Next: takeover, Prev: cp,
	}}, true, nil
}

// planMemberClaims builds the per-member artifacts of an attach: the
// registration claims, the lease-create effects, the sentinel's lease
// list, and the journaled lease targets (the operation journal IS the
// recorded token store). Claim order is id order over all members
// including control, so journal replay is deterministic. cp must already
// carry the effective generation (takeoverCheckpoint bumps it).
func planMemberClaims(ctx context.Context, cp checkpoint.Checkpoint, cfg workspacecfg.Config, site attachSite, prov lease.Process) (claims, leaseEffects []operation.Effect, leaseList []lease.SentinelLease, targets []journaledLeaseTarget, err error) {
	members := sortedCheckpointMembers(cp)
	ordered := make([]checkpoint.Member, 0, len(members))
	for _, m := range members {
		if m.ID == cfg.Control.ID {
			continue
		}
		ordered = append(ordered, m)
	}
	claimTargets := append([]checkpoint.Member(nil), ordered...)
	if control, ok := controlMember(cp, cfg); ok {
		claimTargets = append(claimTargets, control)
	}
	sort.Slice(claimTargets, func(i, j int) bool { return claimTargets[i].ID < claimTargets[j].ID })

	for _, m := range claimTargets {
		slot, err := deriveMemberSlot(ctx, site.runner, site.stateRoot, site.root, m, site.memberRoots[m.ID])
		if err != nil {
			return nil, nil, nil, nil, err
		}
		reg := registration.Registration{
			SchemaVersion: 1,
			WorkspaceID:   cp.WorkspaceID,
			RepositoryID:  m.ID,
			ControlRoot:   site.root,
			MemberRoot:    slot.memberRoot,
			Kind:          m.Kind,
		}
		claims = append(claims, &claimEffect{payload: claimPayload{Path: slot.registrationPath, Registration: reg}})

		token := mustNewToken()
		content := lease.LeaseContent{
			SchemaVersion: 1,
			WorkspaceID:   cp.WorkspaceID,
			RepositoryID:  m.ID,
			WorkID:        cp.Work.ID,
			Generation:    cp.Handoff.Generation,
			Process:       prov,
			RecoveryToken: token,
		}
		if err := content.Validate(); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("handoff: attach: lease for %s: %w", m.ID, err)
		}
		leaseEffects = append(leaseEffects, lease.NewCreateLeaseEffect(slot.leasePath, content))
		leaseList = append(leaseList, lease.SentinelLease{RepositoryID: m.ID, Path: slot.leasePath})
		targets = append(targets, journaledLeaseTarget{RepositoryID: m.ID, Path: slot.leasePath, Token: token})
	}
	return claims, leaseEffects, leaseList, targets, nil
}

// checkAttachable enforces the checkpoint's handoff state for attach.
func checkAttachable(cp checkpoint.Checkpoint, force bool) error {
	switch cp.Handoff.State {
	case checkpoint.HandoffTransferable:
		return nil
	case checkpoint.HandoffConsumed:
		if !force {
			return fmt.Errorf("handoff: checkpoint consumed at generation %d: %w",
				cp.Handoff.Generation, ErrCheckpointConsumed)
		}
		return nil
	default:
		return fmt.Errorf("handoff: checkpoint is %s at generation %d: %w",
			cp.Handoff.State, cp.Handoff.Generation, ErrNotTransferable)
	}
}

// validateStateRoot checks the platform state base for non-git slots.
func validateStateRoot(stateRoot string) error {
	if stateRoot == "" {
		return fmt.Errorf("handoff: state root must not be empty")
	}
	if base := filepath.Base(filepath.Clean(stateRoot)); base == "homonto" {
		return fmt.Errorf("handoff: state root %s already ends in %q; pass the platform state base instead", stateRoot, "homonto")
	}
	return nil
}

// memberSlot is a member's registration and lease file locations on this
// machine, derived from its kind.
type memberSlot struct {
	memberRoot       string
	registrationPath string
	leasePath        string
}

// controlMember returns the checkpoint's control member entry and whether
// the checkpoint carries one; when it does not, the control repository is
// attached without its own claim or lease (engines that enroll control in
// the checkpoint get it claimed like any member).
func controlMember(cp checkpoint.Checkpoint, cfg workspacecfg.Config) (checkpoint.Member, bool) {
	for _, m := range cp.Members {
		if m.ID == cfg.Control.ID {
			return m, true
		}
	}
	return checkpoint.Member{}, false
}

// confirmMappings validates the confirmed mappings against the checkpoint:
// every non-control checkpoint member must be mapped exactly once, no
// unknown or duplicate ids, no foreign path for the control repository, and
// every mapped path must be an existing member root of its declared kind.
// It returns the member roots keyed by repository id.
func confirmMappings(ctx context.Context, cp checkpoint.Checkpoint, cfg workspacecfg.Config, root string, mappings []ConfirmedMapping, runner gitx.Runner) (map[identity.RepositoryID]string, error) {
	needed := make(map[identity.RepositoryID]bool, len(cp.Members))
	for _, m := range cp.Members {
		if m.ID == cfg.Control.ID {
			continue
		}
		needed[m.ID] = true
	}
	byID := make(map[identity.RepositoryID]workspacecfg.Member, len(cfg.Members))
	for _, m := range cfg.Members {
		byID[m.ID] = m
	}

	var problems []string
	seen := make(map[identity.RepositoryID]bool, len(mappings))
	for _, mapping := range mappings {
		if !needed[mapping.RepositoryID] {
			if mapping.RepositoryID == cfg.Control.ID {
				// The control repository is located by the request; an
				// explicit control mapping must agree with it.
				canon, err := workspace.CanonicalPath(mapping.Path)
				if err == nil && canon == root {
					continue
				}
				problems = append(problems, fmt.Sprintf(
					"control repository %s mapped to %s, not the control root %s",
					mapping.RepositoryID, mapping.Path, root))
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"mapping for %s, which is not a checkpoint member", mapping.RepositoryID))
			continue
		}
		if seen[mapping.RepositoryID] {
			problems = append(problems, fmt.Sprintf("duplicate mapping for %s", mapping.RepositoryID))
			continue
		}
		seen[mapping.RepositoryID] = true
	}
	for id := range needed {
		if !seen[id] {
			problems = append(problems, fmt.Sprintf("no confirmed mapping for member %s (%s)", id, memberLabel(byID, id)))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%w: %s", ErrMappingIncomplete, strings.Join(problems, "; "))
	}

	roots := make(map[identity.RepositoryID]string, len(needed)+1)
	roots[cfg.Control.ID] = root
	for _, mapping := range mappings {
		if !needed[mapping.RepositoryID] {
			continue
		}
		canon, err := workspace.CanonicalPath(mapping.Path)
		if err != nil {
			return nil, fmt.Errorf("handoff: mapping of %s: %w", mapping.RepositoryID, err)
		}
		roots[mapping.RepositoryID] = canon
	}

	// Every mapped root must be a usable member root of its declared kind.
	for _, m := range cp.Members {
		if m.ID == cfg.Control.ID {
			continue
		}
		root := roots[m.ID]
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("handoff: member %s at %s is not a directory: %w", m.ID, root, ErrMemberUnusable)
		}
		hasGit := hasGitEntry(root)
		if m.Kind == workspacecfg.KindGit {
			if !hasGit {
				return nil, fmt.Errorf("handoff: member %s at %s has no .git: %w", m.ID, root, ErrMemberUnusable)
			}
			repo, isGit, ierr := gitx.Inspect(ctx, runner, root)
			if ierr != nil || !isGit || repo.TopLevel != root {
				return nil, fmt.Errorf("handoff: member %s at %s is not a repository root: %w", m.ID, root, ErrMemberUnusable)
			}
			continue
		}
		if hasGit {
			return nil, fmt.Errorf("handoff: member %s at %s is declared %s but has .git: %w",
				m.ID, root, m.Kind, ErrMemberUnusable)
		}
	}
	return roots, nil
}

// memberLabel renders a member's configured path for error messages.
func memberLabel(byID map[identity.RepositoryID]workspacecfg.Member, id identity.RepositoryID) string {
	if m, ok := byID[id]; ok {
		return m.Path
	}
	return "unknown path"
}

// hasGitEntry reports whether dir has a .git entry.
func hasGitEntry(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// deriveMemberSlot computes a member's registration and lease paths on this
// machine: git members inside their git common directory, non-git members
// under this machine's state root keyed by canonical path. Non-git
// registrations live in the OLD machine's state root — attach re-claims
// them under the new one, which is exactly why a fresh HOME works.
func deriveMemberSlot(ctx context.Context, runner gitx.Runner, stateRoot, controlRoot string, m checkpoint.Member, memberRoot string) (memberSlot, error) {
	if m.Kind == workspacecfg.KindGit {
		repo, isGit, err := gitx.Inspect(ctx, runner, memberRoot)
		if err != nil {
			return memberSlot{}, fmt.Errorf("handoff: inspect git member %s: %w", m.ID, err)
		}
		if !isGit || repo.TopLevel != memberRoot {
			return memberSlot{}, fmt.Errorf("handoff: member %s at %s is not a repository root: %w", m.ID, memberRoot, ErrMemberUnusable)
		}
		return memberSlot{
			memberRoot:       memberRoot,
			registrationPath: registration.GitRegistrationPath(repo.CommonDir),
			leasePath:        registration.GitLeasePath(repo.CommonDir),
		}, nil
	}
	regPath, err := registration.NonGitRegistrationPath(stateRoot, memberRoot)
	if err != nil {
		return memberSlot{}, fmt.Errorf("handoff: registration path of %s: %w", m.ID, err)
	}
	leasePath, err := registration.NonGitLeasePath(stateRoot, memberRoot)
	if err != nil {
		return memberSlot{}, fmt.Errorf("handoff: lease path of %s: %w", m.ID, err)
	}
	return memberSlot{memberRoot: memberRoot, registrationPath: regPath, leasePath: leasePath}, nil
}

// memberRootsForRebuild adds the control root to the confirmed member
// roots, forming the complete mapping set RebuildRuntime requires.
func memberRootsForRebuild(cp checkpoint.Checkpoint, cfg workspacecfg.Config, memberRoots map[identity.RepositoryID]string, root string) map[identity.RepositoryID]string {
	out := make(map[identity.RepositoryID]string, len(memberRoots)+1)
	for id, p := range memberRoots {
		out[id] = p
	}
	out[cfg.Control.ID] = root
	return out
}
