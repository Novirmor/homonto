package artifact

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/securefs"
)

// docMode is the permission every artifact document is written with.
const docMode fs.FileMode = 0o600

// Service reads and writes workflow documents under one control
// repository root. It is the only writer of artifact files: hosts and
// implementers write through single-use edit grants that name exactly the
// regions the ownership table gives them, and Homonto writes binary-owned
// regions through WriteGenerated. Every access goes through securefs, so a
// planted symlink can never redirect a document read or write.
type Service struct {
	root    string
	journal Journal
	now     Clock
}

// NewService binds a service to the absolute control repository root. The
// journal is required: without a durable grant ledger no grant can be
// issued or verified, and unverifiable edits are never accepted.
func NewService(root string, journal Journal, now Clock) (*Service, error) {
	if root == "" {
		return nil, fmt.Errorf("artifact: root must not be empty")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("artifact: root %q must be an absolute path", root)
	}
	if journal == nil {
		return nil, fmt.Errorf("artifact: %w", ErrNoJournal)
	}
	if now == nil {
		now = time.Now
	}
	return &Service{root: root, journal: journal, now: now}, nil
}

// Root returns the absolute control repository root.
func (s *Service) Root() string { return s.root }

// open opens a fresh confined root on the control repository; one root
// serves one public operation.
func (s *Service) open() (*securefs.Root, error) {
	root, err := securefs.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("artifact: open %s: %w", s.root, err)
	}
	return root, nil
}

// Create writes a new document with meta at rel. The parent directory is
// created if missing — Create is the one place the service makes
// directories, because a work's directory comes into being with its first
// document. Creation is exclusive: an existing path is never overwritten,
// so a second Create can neither clobber a document nor reset its
// metadata.
func (s *Service) Create(ctx context.Context, rel string, meta Metadata) (Snapshot, error) {
	if err := meta.Validate(); err != nil {
		return Snapshot{}, err
	}
	doc := NewDocument(meta)
	rendered, err := Render(doc)
	if err != nil {
		return Snapshot{}, err
	}
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("artifact: mkdir %s: %w", filepath.Dir(rel), err)
	}
	root, err := s.open()
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	if err := root.CreateExclusive(rel, rendered, docMode); err != nil {
		return Snapshot{}, fmt.Errorf("artifact: create %s: %w", rel, err)
	}
	return s.snapshot(Ref{WorkID: meta.WorkID, Kind: meta.Kind, Path: rel}, rendered), nil
}

// Read parses the document at ref and checks that its metadata agrees with
// the reference. A missing document is ErrArtifactMissing; a document
// whose metadata names other work or another kind is ErrRefMismatch.
func (s *Service) Read(ctx context.Context, ref Ref) (Document, error) {
	root, err := s.open()
	if err != nil {
		return Document{}, err
	}
	defer root.Close()
	return s.read(root, ref)
}

// read is Read on an already-open root.
func (s *Service) read(root *securefs.Root, ref Ref) (Document, error) {
	b, err := root.ReadFile(ref.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return Document{}, fmt.Errorf("artifact: %s: %w", ref.Path, ErrArtifactMissing)
	}
	if err != nil {
		return Document{}, fmt.Errorf("artifact: read %s: %w", ref.Path, err)
	}
	doc, err := Parse(b)
	if err != nil {
		return Document{}, err
	}
	if err := matchRef(doc.Metadata, ref); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// matchRef checks a document's metadata against the reference that asked
// for it.
func matchRef(meta Metadata, ref Ref) error {
	if ref.WorkID != "" && meta.WorkID != ref.WorkID {
		return fmt.Errorf("artifact: %s belongs to work %s, not %s: %w",
			ref.Path, meta.WorkID, ref.WorkID, ErrRefMismatch)
	}
	if ref.Kind != "" && meta.Kind != ref.Kind {
		return fmt.Errorf("artifact: %s is a %s document, not a %s: %w",
			ref.Path, meta.Kind, ref.Kind, ErrRefMismatch)
	}
	return nil
}

// GrantEdit issues a single-use permission to edit exactly the requested
// regions of a document during a phase. The owner is never requested: the
// ownership table decides who may write, and a binary-owned region is
// refused outright — Homonto writes those itself through WriteGenerated.
// The grant pins the document's metadata and everything outside the
// granted regions, so AcceptEdit can prove nothing else moved.
func (s *Service) GrantEdit(ctx context.Context, req GrantRequest) (EditGrant, error) {
	root, err := s.open()
	if err != nil {
		return EditGrant{}, err
	}
	defer root.Close()

	doc, err := s.read(root, req.Ref)
	if err != nil {
		return EditGrant{}, err
	}
	owner, allowed, ok := Ownership(req.Ref.Kind, req.Phase)
	if !ok {
		return EditGrant{}, fmt.Errorf("artifact: %s in phase %s: %w", req.Ref.Kind, req.Phase, ErrNotEditable)
	}
	if owner == OwnerBinary {
		return EditGrant{}, fmt.Errorf("artifact: %s in phase %s: %w", req.Ref.Kind, req.Phase, ErrBinaryOwned)
	}
	if err := checkRegions(req.Regions, allowed); err != nil {
		return EditGrant{}, err
	}
	for _, r := range req.Regions {
		if !doc.hasRegion(r) {
			return EditGrant{}, fmt.Errorf("artifact: %s carries no region %q: %w", req.Ref.Path, r, ErrRegionNotGranted)
		}
	}
	if req.ActionID != "" {
		if err := identity.ValidateUUID(string(req.ActionID)); err != nil {
			return EditGrant{}, fmt.Errorf("artifact: grant action_id: %w", err)
		}
	}

	md, err := metaDigest(doc.Metadata)
	if err != nil {
		return EditGrant{}, err
	}
	id, err := identity.NewActionID()
	if err != nil {
		return EditGrant{}, err
	}
	token, err := identity.NewToken()
	if err != nil {
		return EditGrant{}, err
	}
	grant := EditGrant{
		ID:             id,
		ActionID:       req.ActionID,
		Ref:            req.Ref,
		Owner:          owner,
		Phase:          req.Phase,
		Regions:        append([]Region(nil), req.Regions...),
		MetaDigest:     md,
		Before:         immutableBefore(doc, grantedSet(req.Regions)),
		FreshnessToken: token,
	}
	rec := GrantRecord{
		ID:         grant.ID,
		ActionID:   grant.ActionID,
		Ref:        grant.Ref,
		Owner:      grant.Owner,
		Phase:      grant.Phase,
		Regions:    grant.Regions,
		MetaDigest: grant.MetaDigest,
		Before:     grant.Before,
		TokenHash:  tokenDigest(token),
		IssuedAt:   s.now().UTC(),
	}
	if err := s.journal.Put(ctx, rec); err != nil {
		return EditGrant{}, err
	}
	return grant, nil
}

// AcceptEdit accepts the edit a grant permitted. It trusts the journaled
// grant, never the presented one: the presented grant must match the
// record field for field and carry the matching freshness token, and the
// document on disk must differ from the pinned state in the granted
// regions only. Metadata changes, region-set changes, and any byte moved
// outside the grant are refused — and a refused acceptance leaves the
// grant open, because nothing was accepted.
func (s *Service) AcceptEdit(ctx context.Context, grant EditGrant) (Snapshot, error) {
	rec, found, err := s.journal.Lookup(ctx, grant.ID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		return Snapshot{}, fmt.Errorf("artifact: grant %s: %w", grant.ID, ErrUnknownGrant)
	}
	if rec.Consumed {
		return Snapshot{}, fmt.Errorf("artifact: grant %s: %w", grant.ID, ErrGrantConsumed)
	}
	if !tokenMatches(grant.FreshnessToken, rec.TokenHash) {
		return Snapshot{}, fmt.Errorf("artifact: grant %s freshness token: %w", grant.ID, ErrGrantMismatch)
	}
	if grant.ActionID != rec.ActionID || grant.Ref != rec.Ref || grant.Owner != rec.Owner ||
		grant.Phase != rec.Phase || !sameRegions(grant.Regions, rec.Regions) ||
		grant.MetaDigest != rec.MetaDigest || grant.Before != rec.Before {
		return Snapshot{}, fmt.Errorf("artifact: grant %s: %w", grant.ID, ErrGrantMismatch)
	}

	root, err := s.open()
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	doc, err := s.read(root, rec.Ref)
	if err != nil {
		return Snapshot{}, err
	}
	md, err := metaDigest(doc.Metadata)
	if err != nil {
		return Snapshot{}, err
	}
	if md != rec.MetaDigest {
		return Snapshot{}, fmt.Errorf("artifact: %s: %w", rec.Ref.Path, ErrMetadataChanged)
	}
	want := regionsOf(rec.Ref.Kind)
	if len(doc.Regions) != len(want) {
		return Snapshot{}, fmt.Errorf("artifact: %s carries %d regions, want %d: %w",
			rec.Ref.Path, len(doc.Regions), len(want), ErrRegionSetChanged)
	}
	for i, r := range want {
		if doc.Regions[i].Region != r {
			return Snapshot{}, fmt.Errorf("artifact: %s region %d is %q, want %q: %w",
				rec.Ref.Path, i, doc.Regions[i].Region, r, ErrRegionSetChanged)
		}
	}
	if got := immutableBefore(doc, grantedSet(rec.Regions)); got != rec.Before {
		return Snapshot{}, fmt.Errorf("artifact: %s: %w", rec.Ref.Path, ErrImmutableRegion)
	}

	// Rewrite the document in canonical form so an accepted edit is always
	// byte-identical to what Render produces: a host may have left
	// non-canonical blank lines inside its own regions, and every later
	// digest must be taken over the canonical bytes.
	rendered, err := Render(doc)
	if err != nil {
		return Snapshot{}, err
	}
	if err := root.WriteAtomic(rec.Ref.Path, rendered, docMode); err != nil {
		return Snapshot{}, fmt.Errorf("artifact: write %s: %w", rec.Ref.Path, err)
	}
	snap := s.snapshot(rec.Ref, rendered)
	if err := s.journal.Consume(ctx, rec.ID, snap.At, snap.Digest); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// WriteGenerated replaces binary-owned regions of an existing document.
// It is the only write path for regions the ownership table gives the
// binary — task checkoffs, evidence, verification, the record — and it
// refuses any region a phase does not give the binary, so a generated
// write can never be used to launder a host edit.
func (s *Service) WriteGenerated(ctx context.Context, ref Ref, phase Phase, regions []RegionContent) (Snapshot, error) {
	owner, allowed, ok := Ownership(ref.Kind, phase)
	if !ok {
		return Snapshot{}, fmt.Errorf("artifact: %s in phase %s: %w", ref.Kind, phase, ErrNotEditable)
	}
	if owner != OwnerBinary {
		return Snapshot{}, fmt.Errorf("artifact: %s in phase %s is owned by the %s: %w",
			ref.Kind, phase, owner, ErrNotEditable)
	}
	want := make([]Region, len(regions))
	for i, rc := range regions {
		want[i] = rc.Region
	}
	if err := checkRegions(want, allowed); err != nil {
		return Snapshot{}, err
	}

	root, err := s.open()
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	doc, err := s.read(root, ref)
	if err != nil {
		return Snapshot{}, err
	}
	for _, rc := range regions {
		if !doc.hasRegion(rc.Region) {
			return Snapshot{}, fmt.Errorf("artifact: %s carries no region %q: %w", ref.Path, rc.Region, ErrRegionNotGranted)
		}
	}
	updated := doc
	updated.Regions = append([]RegionContent(nil), doc.Regions...)
	for _, rc := range regions {
		for i := range updated.Regions {
			if updated.Regions[i].Region == rc.Region {
				updated.Regions[i].Content = rc.Content
			}
		}
	}
	rendered, err := Render(updated)
	if err != nil {
		return Snapshot{}, err
	}
	if err := root.WriteAtomic(ref.Path, rendered, docMode); err != nil {
		return Snapshot{}, fmt.Errorf("artifact: write %s: %w", ref.Path, err)
	}
	return s.snapshot(ref, rendered), nil
}

// Digest returns the current document's canonical digest, the value
// downstream evidence pins an artifact by.
func (s *Service) Digest(ctx context.Context, ref Ref) (Snapshot, error) {
	root, err := s.open()
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	doc, err := s.read(root, ref)
	if err != nil {
		return Snapshot{}, err
	}
	rendered, err := Render(doc)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ref, rendered), nil
}

// snapshot stamps a rendered document with its digest and the service
// clock.
func (s *Service) snapshot(ref Ref, rendered []byte) Snapshot {
	return Snapshot{Ref: ref, Digest: DocumentDigest(rendered), At: s.now().UTC()}
}
