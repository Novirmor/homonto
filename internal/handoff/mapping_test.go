package handoff

import (
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// mappingFixture builds one checkpoint + config with a git member (b) and a
// non-git member (a) beside the control repository.
type mappingFixture struct {
	cp        checkpoint.Checkpoint
	cfg       workspacecfg.Config
	memberA   identity.RepositoryID
	memberB   identity.RepositoryID
	controlID identity.RepositoryID
}

func mustFingerprint(t *testing.T, cfg workspacecfg.Config) fingerprint.Digest {
	t.Helper()
	d, err := workspacecfg.Fingerprint(cfg)
	if err != nil {
		t.Fatalf("handoff: fingerprint: %v", err)
	}
	return d
}

func mustHex(t *testing.T, seed byte) string {
	t.Helper()
	out := make([]byte, 40)
	for i := range out {
		out[i] = "0123456789abcdef"[int(seed)%16]
	}
	return string(out)
}

func mustDigest(t *testing.T) fingerprint.Digest {
	t.Helper()
	return fingerprintOf(t.Name())
}

func newMappingFixture(t *testing.T) mappingFixture {
	t.Helper()
	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("handoff: ws id: %v", err)
	}
	controlID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: control id: %v", err)
	}
	memberA, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: member a id: %v", err)
	}
	memberB, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: member b id: %v", err)
	}
	cfg := workspacecfg.Config{
		SchemaVersion: 1,
		Workspace:     workspacecfg.Workspace{ID: wsID, Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: controlID, Path: "."},
		Members: []workspacecfg.Member{
			{ID: controlID, Path: ".", Kind: workspacecfg.KindGit},
			{ID: memberA, Path: "member-a", Kind: workspacecfg.KindNonGit},
			{ID: memberB, Path: "member-b", Kind: workspacecfg.KindGit, Remotes: []string{"https://example.com/b.git"}},
		},
	}
	cp := checkpoint.Checkpoint{
		SchemaVersion:     checkpoint.CurrentSchemaVersion,
		WorkspaceID:       wsID,
		ConfigFingerprint: mustFingerprint(t, cfg),
		Members: []checkpoint.Member{
			{ID: controlID, Kind: workspacecfg.KindGit, BaseBranch: "main", BaseCommit: mustHex(t, 1), IntegrationBranch: "main", SourceFingerprint: mustDigest(t)},
			{ID: memberA, Kind: workspacecfg.KindNonGit, SourceFingerprint: mustDigest(t)},
			{ID: memberB, Kind: workspacecfg.KindGit, BaseBranch: "main", BaseCommit: mustHex(t, 1), IntegrationBranch: "main", SourceFingerprint: mustDigest(t)},
		},
		Handoff: checkpoint.Handoff{State: checkpoint.HandoffTransferable, Generation: 2, TransferID: mustToken(t)},
	}
	return mappingFixture{cp: cp, cfg: cfg, memberA: memberA, memberB: memberB, controlID: controlID}
}

func findProposal(t *testing.T, proposals []Proposal, id identity.RepositoryID) Proposal {
	t.Helper()
	for _, p := range proposals {
		if p.RepositoryID == id {
			return p
		}
	}
	t.Fatalf("handoff: no proposal for %s in %+v", id, proposals)
	return Proposal{}
}

func TestProposeMappingsExact(t *testing.T) {
	f := newMappingFixture(t)
	candidates := []workspace.Candidate{
		{Path: "/new/ws/member-a", Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
		{Path: "/new/ws/member-b", Kind: workspacecfg.KindGit, Git: &gitx.Repository{
			TopLevel: "/new/ws/member-b",
			Remotes:  []gitx.Remote{{Name: "origin", URL: "https://example.com/b.git"}},
		}},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	if len(proposals) != 2 {
		t.Fatalf("handoff: proposals = %d, want 2 (control excluded)", len(proposals))
	}
	// Sorted by repository id.
	if proposals[0].RepositoryID > proposals[1].RepositoryID {
		t.Errorf("handoff: proposals not sorted by repository id")
	}
	for _, p := range proposals {
		if p.Status != StatusExact {
			t.Errorf("handoff: proposal %s = %s (%v), want exact", p.RepositoryID, p.Status, p.Reasons)
		}
		if len(p.Candidates) != 1 {
			t.Errorf("handoff: proposal %s candidates = %d, want 1", p.RepositoryID, len(p.Candidates))
		}
	}
}

func TestProposeMappingsKindMismatchIsNotExact(t *testing.T) {
	f := newMappingFixture(t)
	// member-b is declared git; a non-git candidate at its workspace path
	// cannot be an exact match and carries no remote evidence.
	candidates := []workspace.Candidate{
		{Path: "/new/ws/member-a", Kind: workspacecfg.KindNonGit},
		{Path: "/new/ws/member-b", Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	p := findProposal(t, proposals, f.memberB)
	if p.Status == StatusExact {
		t.Fatalf("handoff: kind-mismatched proposal is exact")
	}
	if p.Status != StatusMissing {
		t.Errorf("handoff: kind-mismatched proposal = %s, want missing", p.Status)
	}
	if len(p.Reasons) == 0 {
		t.Errorf("handoff: kind-mismatched proposal carries no reasons")
	}
}

func TestProposeMappingsChanged(t *testing.T) {
	f := newMappingFixture(t)
	// The candidate lives at a different path but shares the declared remote.
	candidates := []workspace.Candidate{
		{Path: "/new/ws/member-a", Kind: workspacecfg.KindNonGit},
		{Path: "/elsewhere/b", Kind: workspacecfg.KindGit, Git: &gitx.Repository{
			TopLevel: "/elsewhere/b",
			Remotes:  []gitx.Remote{{Name: "origin", URL: "https://example.com/b.git"}},
		}},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	p := findProposal(t, proposals, f.memberB)
	if p.Status != StatusChanged {
		t.Fatalf("handoff: remote-matching proposal = %s (%v), want changed", p.Status, p.Reasons)
	}
}

func TestProposeMappingsChangedByKindAtSamePath(t *testing.T) {
	f := newMappingFixture(t)
	// member-b is declared git at "member-b"; a candidate sits at the same
	// workspace path but with the wrong kind, and still carries the
	// declared remote — the member did not move, its kind changed, so the
	// proposal is changed and the reason names the kind, not the path.
	candidates := []workspace.Candidate{
		{Path: "/new/ws/member-a", Kind: workspacecfg.KindNonGit},
		{Path: "/new/ws/member-b", Kind: workspacecfg.KindNonGit, Manifest: "go.mod", Git: &gitx.Repository{
			TopLevel: "/new/ws/member-b",
			Remotes:  []gitx.Remote{{Name: "origin", URL: "https://example.com/b.git"}},
		}},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	p := findProposal(t, proposals, f.memberB)
	if p.Status != StatusChanged {
		t.Fatalf("handoff: kind-mismatched proposal = %s (%v), want changed", p.Status, p.Reasons)
	}
	if len(p.Reasons) == 0 || !strings.Contains(p.Reasons[0], "kind") {
		t.Errorf("handoff: changed reason = %v, want it to name the kind", p.Reasons)
	}
}

func TestProposeMappingsAmbiguousByRemotes(t *testing.T) {
	f := newMappingFixture(t)
	remote := []gitx.Remote{{Name: "origin", URL: "https://example.com/b.git"}}
	candidates := []workspace.Candidate{
		{Path: "/new/ws/member-a", Kind: workspacecfg.KindNonGit},
		{Path: "/one/b", Kind: workspacecfg.KindGit, Git: &gitx.Repository{TopLevel: "/one/b", Remotes: remote}},
		{Path: "/two/b", Kind: workspacecfg.KindGit, Git: &gitx.Repository{TopLevel: "/two/b", Remotes: remote}},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	p := findProposal(t, proposals, f.memberB)
	if p.Status != StatusAmbiguous {
		t.Fatalf("handoff: two equal remote matches = %s, want ambiguous", p.Status)
	}
}

func TestProposeMappingsAmbiguousByPath(t *testing.T) {
	f := newMappingFixture(t)
	// Two hosts each carry a non-git member at the workspace-relative path.
	candidates := []workspace.Candidate{
		{Path: "/one/member-a", Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
		{Path: "/two/member-a", Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	p := findProposal(t, proposals, f.memberA)
	if p.Status != StatusAmbiguous {
		t.Fatalf("handoff: two path matches = %s, want ambiguous", p.Status)
	}
}

func TestProposeMappingsMissing(t *testing.T) {
	f := newMappingFixture(t)
	candidates := []workspace.Candidate{
		{Path: "/new/ws/other", Kind: workspacecfg.KindNonGit},
	}
	proposals := ProposeMappings(f.cp, f.cfg, candidates)
	if len(proposals) != 2 {
		t.Fatalf("handoff: proposals = %d, want 2", len(proposals))
	}
	for _, p := range proposals {
		if p.Status != StatusMissing {
			t.Errorf("handoff: proposal %s = %s, want missing", p.RepositoryID, p.Status)
		}
		if len(p.Candidates) != 0 {
			t.Errorf("handoff: missing proposal carries candidates")
		}
	}
}

func TestProposeMappingsExcludesControl(t *testing.T) {
	f := newMappingFixture(t)
	proposals := ProposeMappings(f.cp, f.cfg, nil)
	for _, p := range proposals {
		if p.RepositoryID == f.controlID {
			t.Fatalf("handoff: control repository received a proposal")
		}
	}
}
