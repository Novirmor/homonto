package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// fixedNow is the clock every service test injects; snapshots must never
// depend on the wall clock.
var fixedNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

// newService returns a service over a fresh control root backed by a real
// migrated database, plus the root path.
func newService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(root, "homonto.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	journal, err := NewStoreJournal(db)
	if err != nil {
		t.Fatalf("NewStoreJournal: %v", err)
	}
	svc, err := NewService(root, journal, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, root
}

func mustWorkID(t *testing.T) identity.WorkID {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	return id
}

// newTask creates an active task document and returns its ref.
func newTask(t *testing.T, s *Service, name string) Ref {
	t.Helper()
	workID := mustWorkID(t)
	path, err := Path(name, KindTaskDocument)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := s.Create(t.Context(), path, Metadata{
		Schema: MetadataSchema, WorkID: workID, Name: name, Kind: KindTaskDocument,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return Ref{WorkID: workID, Kind: KindTaskDocument, Path: path}
}

// newProposal creates an active proposal document and returns its ref.
func newProposal(t *testing.T, s *Service, name string) Ref {
	t.Helper()
	workID := mustWorkID(t)
	path, err := Path(name, KindProposal)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := s.Create(t.Context(), path, Metadata{
		Schema: MetadataSchema, WorkID: workID, Name: name, Kind: KindProposal,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return Ref{WorkID: workID, Kind: KindProposal, Path: path}
}

// editRegion rewrites one region of the document on disk the way a host
// would: parse, replace, render, write.
func editRegion(t *testing.T, root string, ref Ref, region Region, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(ref.Path))
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", ref.Path, err)
	}
	doc, err := Parse(b)
	if err != nil {
		t.Fatalf("parse %s: %v", ref.Path, err)
	}
	found := false
	for i := range doc.Regions {
		if doc.Regions[i].Region == region {
			doc.Regions[i].Content = []byte(content)
			found = true
		}
	}
	if !found {
		t.Fatalf("document has no region %q", region)
	}
	rendered, err := Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write %s: %v", ref.Path, err)
	}
}

// overwrite replaces the raw bytes of a document, bypassing every check —
// the hostile-host simulation.
func overwrite(t *testing.T, root string, ref Ref, raw string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(ref.Path))
	if err := os.WriteFile(abs, []byte(raw), 0o600); err != nil {
		t.Fatalf("write %s: %v", ref.Path, err)
	}
}

func TestNewServiceRequiresAbsoluteRootAndJournal(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	journal, err := NewStoreJournal(db)
	if err != nil {
		t.Fatalf("NewStoreJournal: %v", err)
	}
	if _, err := NewService("relative", journal, nil); err == nil {
		t.Fatal("NewService(relative) = nil error, want rejection")
	}
	if _, err := NewService(t.TempDir(), nil, nil); !errors.Is(err, ErrNoJournal) {
		t.Fatalf("NewService(nil journal) error = %v, want ErrNoJournal", err)
	}
	if _, err := NewStoreJournal(nil); !errors.Is(err, ErrNoJournal) {
		t.Fatalf("NewStoreJournal(nil) error = %v, want ErrNoJournal", err)
	}
}

func TestCreateIsExclusiveAndReadable(t *testing.T) {
	svc, _ := newService(t)
	ref := newTask(t, svc, "fix-login")

	doc, err := svc.Read(t.Context(), ref)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(doc.Regions) != 3 {
		t.Fatalf("task document has %d regions, want 3", len(doc.Regions))
	}
	// A second Create must not clobber the document or reset its metadata.
	err = func() error {
		_, err := svc.Create(t.Context(), ref.Path, Metadata{
			Schema: MetadataSchema, WorkID: mustWorkID(t), Name: "fix-login", Kind: KindTaskDocument,
		})
		return err
	}()
	if err == nil {
		t.Fatal("second Create = nil error, want refusal")
	}
	after, err := svc.Read(t.Context(), ref)
	if err != nil {
		t.Fatalf("Read after refused Create: %v", err)
	}
	if after.Metadata.WorkID != ref.WorkID {
		t.Fatal("refused Create changed the document's identity")
	}
}

func TestReadRefusesMissingAndMismatchedDocuments(t *testing.T) {
	svc, _ := newService(t)
	ref := newTask(t, svc, "fix-login")

	missing := Ref{WorkID: ref.WorkID, Kind: KindTaskDocument, Path: "active/fix-login/plan.md"}
	if _, err := svc.Read(t.Context(), missing); !errors.Is(err, ErrArtifactMissing) {
		t.Fatalf("Read(missing) error = %v, want ErrArtifactMissing", err)
	}
	otherWork := ref
	otherWork.WorkID = mustWorkID(t)
	if _, err := svc.Read(t.Context(), otherWork); !errors.Is(err, ErrRefMismatch) {
		t.Fatalf("Read(other work) error = %v, want ErrRefMismatch", err)
	}
	otherKind := ref
	otherKind.Kind = KindProposal
	if _, err := svc.Read(t.Context(), otherKind); !errors.Is(err, ErrRefMismatch) {
		t.Fatalf("Read(other kind) error = %v, want ErrRefMismatch", err)
	}
}

// TestOwnershipTableMatchesSpec pins the ownership rules the spec states,
// including the pairs nobody may write.
func TestOwnershipTableMatchesSpec(t *testing.T) {
	tests := []struct {
		kind    Kind
		phase   Phase
		owner   Owner
		regions []Region
	}{
		{KindTaskDocument, PhasePlan, OwnerHost, []Region{RegionTaskGoal, RegionTaskChecklist}},
		{KindTaskDocument, PhaseDo, OwnerBinary, []Region{RegionTaskChecklist}},
		{KindTaskDocument, PhaseDone, OwnerBinary, []Region{RegionTaskEvidence}},
		{KindProposal, PhaseOpen, OwnerHost, []Region{RegionWholeDocument}},
		{KindDesign, PhaseDesign, OwnerHost, []Region{RegionWholeDocument}},
		{KindTasks, PhaseDesign, OwnerHost, []Region{RegionWholeDocument}},
		{KindTasks, PhaseOpen, OwnerHost, []Region{RegionWholeDocument}},
		{KindTasks, PhaseBuild, OwnerBinary, []Region{RegionWholeDocument}},
		{KindPlan, PhaseBuild, OwnerHost, []Region{RegionWholeDocument}},
		{KindFix, PhaseOpen, OwnerHost, []Region{RegionWholeDocument}},
		{KindTweak, PhaseOpen, OwnerHost, []Region{RegionWholeDocument}},
		{KindVerification, PhaseVerify, OwnerBinary, []Region{RegionWholeDocument}},
		{KindRecord, PhaseClose, OwnerBinary, []Region{RegionWholeDocument}},
		{KindADR, PhaseClose, OwnerImplementer, []Region{RegionWholeDocument}},
	}
	for _, tt := range tests {
		owner, regions, ok := Ownership(tt.kind, tt.phase)
		if !ok {
			t.Errorf("Ownership(%s, %s) not found", tt.kind, tt.phase)
			continue
		}
		if owner != tt.owner || !sameRegions(regions, tt.regions) {
			t.Errorf("Ownership(%s, %s) = %s %v, want %s %v", tt.kind, tt.phase, owner, regions, tt.owner, tt.regions)
		}
	}
	// Nobody writes these.
	for _, tt := range []struct {
		kind  Kind
		phase Phase
	}{
		{KindTaskDocument, PhaseOpen},
		{KindProposal, PhaseBuild},
		{KindDesign, PhaseOpen},
		{KindPlan, PhaseDesign},
		{KindVerification, PhaseClose},
		{KindRecord, PhaseVerify},
		{KindPresetTasks, PhaseOpen},
		{KindPresetTasks, PhaseBuild},
	} {
		if _, _, ok := Ownership(tt.kind, tt.phase); ok {
			t.Errorf("Ownership(%s, %s) is editable, want nobody", tt.kind, tt.phase)
		}
	}
}

func TestGrantEditRefusesWhatTheTableForbids(t *testing.T) {
	svc, _ := newService(t)
	task := newTask(t, svc, "fix-login")
	proposal := newProposal(t, svc, "rework-catalog")

	tests := []struct {
		name    string
		ref     Ref
		phase   Phase
		regions []Region
		want    error
	}{
		{"kind not editable in that phase", task, PhaseOpen, []Region{RegionTaskGoal}, ErrNotEditable},
		{"binary-owned phase", task, PhaseDo, []Region{RegionTaskChecklist}, ErrBinaryOwned},
		{"binary-owned evidence", task, PhaseDone, []Region{RegionTaskEvidence}, ErrBinaryOwned},
		{"region not granted in phase", task, PhasePlan, []Region{RegionTaskEvidence}, ErrRegionNotGranted},
		{"no regions requested", task, PhasePlan, nil, ErrRegionNotGranted},
		{"unknown region", task, PhasePlan, []Region{Region("task-notes")}, ErrRegionNotGranted},
		{"duplicate region", task, PhasePlan, []Region{RegionTaskGoal, RegionTaskGoal}, ErrRegionNotGranted},
		{"wrong region for a whole-document kind", proposal, PhaseOpen, []Region{RegionTaskGoal}, ErrRegionNotGranted},
		{"proposal not editable in build", proposal, PhaseBuild, []Region{RegionWholeDocument}, ErrNotEditable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.GrantEdit(t.Context(), GrantRequest{Ref: tt.ref, Phase: tt.phase, Regions: tt.regions})
			if !errors.Is(err, tt.want) {
				t.Fatalf("GrantEdit error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGrantEditThenAcceptEditAcceptsTheGrantedEdit(t *testing.T) {
	svc, root := newService(t)
	ref := newTask(t, svc, "fix-login")

	grant, err := svc.GrantEdit(t.Context(), GrantRequest{
		Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal, RegionTaskChecklist},
	})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	if grant.Owner != OwnerHost {
		t.Fatalf("grant.Owner = %q, want host", grant.Owner)
	}
	if err := identity.ValidateToken(string(grant.FreshnessToken)); err != nil {
		t.Fatalf("grant token: %v", err)
	}

	editRegion(t, root, ref, RegionTaskGoal, "Make login work.\n")
	editRegion(t, root, ref, RegionTaskChecklist, "- [ ] reproduce\n- [ ] fix\n")

	snap, err := svc.AcceptEdit(t.Context(), grant)
	if err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	if snap.Ref != ref {
		t.Fatalf("snapshot ref = %+v, want %+v", snap.Ref, ref)
	}
	if !snap.At.Equal(fixedNow) {
		t.Fatalf("snapshot time = %v, want the injected clock %v", snap.At, fixedNow)
	}
	if err := snap.Digest.Validate(); err != nil {
		t.Fatalf("snapshot digest: %v", err)
	}
}

func TestAcceptEditIsSingleUse(t *testing.T) {
	svc, root := newService(t)
	ref := newTask(t, svc, "fix-login")
	grant, err := svc.GrantEdit(t.Context(), GrantRequest{Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal}})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	editRegion(t, root, ref, RegionTaskGoal, "Make login work.\n")
	if _, err := svc.AcceptEdit(t.Context(), grant); err != nil {
		t.Fatalf("first AcceptEdit: %v", err)
	}
	if _, err := svc.AcceptEdit(t.Context(), grant); !errors.Is(err, ErrGrantConsumed) {
		t.Fatalf("second AcceptEdit error = %v, want ErrGrantConsumed", err)
	}
}

func TestAcceptEditRefusesForgedAndUnknownGrants(t *testing.T) {
	svc, root := newService(t)
	ref := newTask(t, svc, "fix-login")
	grant, err := svc.GrantEdit(t.Context(), GrantRequest{
		Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal},
	})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	editRegion(t, root, ref, RegionTaskGoal, "Make login work.\n")

	unknownID, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("NewActionID: %v", err)
	}
	otherToken, err := identity.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	widened := grant
	widened.Regions = []Region{RegionTaskGoal, RegionTaskChecklist}
	wrongPhase := grant
	wrongPhase.Phase = PhaseDo
	wrongOwner := grant
	wrongOwner.Owner = OwnerBinary
	forgedBefore := grant
	forgedBefore.Before = DocumentDigest([]byte("anything"))
	wrongToken := grant
	wrongToken.FreshnessToken = otherToken
	unknown := grant
	unknown.ID = unknownID

	tests := []struct {
		name  string
		grant EditGrant
		want  error
	}{
		{"unknown grant id", unknown, ErrUnknownGrant},
		{"wrong freshness token", wrongToken, ErrGrantMismatch},
		{"regions widened after issue", widened, ErrGrantMismatch},
		{"phase changed after issue", wrongPhase, ErrGrantMismatch},
		{"owner changed after issue", wrongOwner, ErrGrantMismatch},
		{"before digest forged", forgedBefore, ErrGrantMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.AcceptEdit(t.Context(), tt.grant); !errors.Is(err, tt.want) {
				t.Fatalf("AcceptEdit error = %v, want %v", err, tt.want)
			}
		})
	}
	// Every refusal above must have left the grant open, not consumed.
	if _, err := svc.AcceptEdit(t.Context(), grant); err != nil {
		t.Fatalf("AcceptEdit after refusals: %v", err)
	}
}

func TestAcceptEditRefusesEditsOutsideTheGrant(t *testing.T) {
	svc, root := newService(t)

	t.Run("region outside the grant changed", func(t *testing.T) {
		ref := newTask(t, svc, "fix-login")
		grant, err := svc.GrantEdit(t.Context(), GrantRequest{
			Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal},
		})
		if err != nil {
			t.Fatalf("GrantEdit: %v", err)
		}
		editRegion(t, root, ref, RegionTaskGoal, "goal\n")
		editRegion(t, root, ref, RegionTaskChecklist, "- [ ] snuck in\n")
		if _, err := svc.AcceptEdit(t.Context(), grant); !errors.Is(err, ErrImmutableRegion) {
			t.Fatalf("AcceptEdit error = %v, want ErrImmutableRegion", err)
		}
	})

	t.Run("binary-owned evidence region changed", func(t *testing.T) {
		ref := newTask(t, svc, "fix-cache")
		grant, err := svc.GrantEdit(t.Context(), GrantRequest{
			Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal, RegionTaskChecklist},
		})
		if err != nil {
			t.Fatalf("GrantEdit: %v", err)
		}
		editRegion(t, root, ref, RegionTaskEvidence, "all checks passed\n")
		if _, err := svc.AcceptEdit(t.Context(), grant); !errors.Is(err, ErrImmutableRegion) {
			t.Fatalf("AcceptEdit error = %v, want ErrImmutableRegion", err)
		}
	})

	t.Run("metadata rewritten", func(t *testing.T) {
		ref := newTask(t, svc, "fix-timeout")
		grant, err := svc.GrantEdit(t.Context(), GrantRequest{
			Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal},
		})
		if err != nil {
			t.Fatalf("GrantEdit: %v", err)
		}
		doc, err := svc.Read(t.Context(), ref)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		doc.Metadata.Name = "renamed-work"
		rendered, err := Render(doc)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		overwrite(t, root, ref, string(rendered))
		if _, err := svc.AcceptEdit(t.Context(), grant); !errors.Is(err, ErrMetadataChanged) {
			t.Fatalf("AcceptEdit error = %v, want ErrMetadataChanged", err)
		}
	})

	t.Run("region markers destroyed", func(t *testing.T) {
		ref := newTask(t, svc, "fix-retry")
		grant, err := svc.GrantEdit(t.Context(), GrantRequest{
			Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal},
		})
		if err != nil {
			t.Fatalf("GrantEdit: %v", err)
		}
		meta, err := RenderMetadata(Metadata{
			Schema: MetadataSchema, WorkID: ref.WorkID, Name: "fix-retry", Kind: KindTaskDocument,
		})
		if err != nil {
			t.Fatalf("RenderMetadata: %v", err)
		}
		overwrite(t, root, ref, string(meta)+"\nfree-form prose, no regions\n")
		if _, err := svc.AcceptEdit(t.Context(), grant); !errors.Is(err, ErrTamperedDocument) {
			t.Fatalf("AcceptEdit error = %v, want ErrTamperedDocument", err)
		}
	})

	t.Run("document deleted", func(t *testing.T) {
		ref := newTask(t, svc, "fix-deleted")
		grant, err := svc.GrantEdit(t.Context(), GrantRequest{
			Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal},
		})
		if err != nil {
			t.Fatalf("GrantEdit: %v", err)
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(ref.Path))); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := svc.AcceptEdit(t.Context(), grant); !errors.Is(err, ErrArtifactMissing) {
			t.Fatalf("AcceptEdit error = %v, want ErrArtifactMissing", err)
		}
	})
}

// TestAcceptEditAcceptsAnUnchangedDocument records the deliberate choice:
// a grant that was never used produces a valid snapshot rather than an
// error. The engine, not this layer, decides whether "no edit" advances.
func TestAcceptEditAcceptsAnUnchangedDocument(t *testing.T) {
	svc, _ := newService(t)
	ref := newTask(t, svc, "fix-login")
	grant, err := svc.GrantEdit(t.Context(), GrantRequest{Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal}})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	if _, err := svc.AcceptEdit(t.Context(), grant); err != nil {
		t.Fatalf("AcceptEdit of an unchanged document: %v", err)
	}
}

// TestAcceptEditCanonicalizesTheAcceptedDocument proves an accepted edit
// is stored exactly as Render produces it, so later digests are stable.
func TestAcceptEditCanonicalizesTheAcceptedDocument(t *testing.T) {
	svc, root := newService(t)
	ref := newProposal(t, svc, "rework-catalog")
	grant, err := svc.GrantEdit(t.Context(), GrantRequest{
		Ref: ref, Phase: PhaseOpen, Regions: []Region{RegionWholeDocument},
	})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	// A host leaves trailing blank lines and no final newline.
	doc, err := svc.Read(t.Context(), ref)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	rendered, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	overwrite(t, root, ref, string(rendered)+"\n\n## Why\n\nBecause.\n\n\n")

	snap, err := svc.AcceptEdit(t.Context(), grant)
	if err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref.Path)))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if snap.Digest != DocumentDigest(onDisk) {
		t.Fatal("snapshot digest does not match the bytes on disk")
	}
	if strings.HasSuffix(string(onDisk), "\n\n\n") {
		t.Fatalf("accepted document was not canonicalized:\n%q", onDisk)
	}
	if !strings.Contains(string(onDisk), "## Why") {
		t.Fatal("accepted document lost the host's edit")
	}
}

func TestWriteGeneratedIsBinaryOnly(t *testing.T) {
	svc, _ := newService(t)
	task := newTask(t, svc, "fix-login")

	// The host-owned Plan phase is not a generated-write phase.
	if _, err := svc.WriteGenerated(t.Context(), task, PhasePlan, []RegionContent{
		{Region: RegionTaskGoal, Content: []byte("goal\n")},
	}); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("WriteGenerated in a host phase = %v, want ErrNotEditable", err)
	}
	// Do gives the binary the checklist only, never the goal.
	if _, err := svc.WriteGenerated(t.Context(), task, PhaseDo, []RegionContent{
		{Region: RegionTaskGoal, Content: []byte("goal\n")},
	}); !errors.Is(err, ErrRegionNotGranted) {
		t.Fatalf("WriteGenerated of a non-binary region = %v, want ErrRegionNotGranted", err)
	}
	// And the binary write it does own succeeds.
	if _, err := svc.WriteGenerated(t.Context(), task, PhaseDo, []RegionContent{
		{Region: RegionTaskChecklist, Content: []byte("- [ ] a\n")},
	}); err != nil {
		t.Fatalf("WriteGenerated of the checklist: %v", err)
	}
	doc, err := svc.Read(t.Context(), task)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(doc.Region(RegionTaskChecklist)) != "- [ ] a\n" {
		t.Fatalf("checklist = %q", doc.Region(RegionTaskChecklist))
	}
}

// TestGrantsAreScopedToTheirDocument proves a grant for one document
// cannot be presented for another.
func TestGrantsAreScopedToTheirDocument(t *testing.T) {
	svc, root := newService(t)
	a := newTask(t, svc, "fix-login")
	b := newTask(t, svc, "fix-cache")
	grant, err := svc.GrantEdit(t.Context(), GrantRequest{Ref: a, Phase: PhasePlan, Regions: []Region{RegionTaskGoal}})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	editRegion(t, root, b, RegionTaskGoal, "edited the wrong document\n")
	redirected := grant
	redirected.Ref = b
	if _, err := svc.AcceptEdit(t.Context(), redirected); !errors.Is(err, ErrGrantMismatch) {
		t.Fatalf("AcceptEdit(redirected) error = %v, want ErrGrantMismatch", err)
	}
}

// TestGrantLinksItsWorkflowAction proves the optional action link survives
// the journal round trip and is part of what AcceptEdit matches.
func TestGrantLinksItsWorkflowAction(t *testing.T) {
	svc, _ := newService(t)
	ref := newTask(t, svc, "fix-login")
	actionID, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("NewActionID: %v", err)
	}
	grant, err := svc.GrantEdit(t.Context(), GrantRequest{
		Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal}, ActionID: actionID,
	})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}
	if grant.ActionID != actionID {
		t.Fatalf("grant.ActionID = %q, want %q", grant.ActionID, actionID)
	}
	stripped := grant
	stripped.ActionID = ""
	if _, err := svc.AcceptEdit(t.Context(), stripped); !errors.Is(err, ErrGrantMismatch) {
		t.Fatalf("AcceptEdit(stripped action link) error = %v, want ErrGrantMismatch", err)
	}
	if _, err := svc.AcceptEdit(t.Context(), grant); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
}

func TestGrantEditRejectsMalformedActionID(t *testing.T) {
	svc, _ := newService(t)
	ref := newTask(t, svc, "fix-login")
	if _, err := svc.GrantEdit(t.Context(), GrantRequest{
		Ref: ref, Phase: PhasePlan, Regions: []Region{RegionTaskGoal}, ActionID: "not-a-uuid",
	}); err == nil {
		t.Fatal("GrantEdit with a malformed action id = nil error, want rejection")
	}
}
