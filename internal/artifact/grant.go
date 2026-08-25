package artifact

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Typed grant errors. GrantEdit refuses what the ownership table forbids;
// AcceptEdit refuses everything that is not exactly the granted edit.
var (
	// ErrNotEditable: no one may edit the kind in the phase (or the
	// kind/phase pair is not part of any workflow).
	ErrNotEditable = errors.New("artifact: kind is not editable in that phase")
	// ErrBinaryOwned: the regions are binary-owned; hosts and implementers
	// write nothing there — the binary writes via WriteGenerated.
	ErrBinaryOwned = errors.New("artifact: regions are binary-owned; use WriteGenerated")
	// ErrRegionNotGranted: the requested regions exceed what the phase
	// grants the owner.
	ErrRegionNotGranted = errors.New("artifact: region is not granted in that phase")
	// ErrArtifactMissing: the referenced document does not exist.
	ErrArtifactMissing = errors.New("artifact: no document at that path")
	// ErrRefMismatch: the document's metadata does not match the request.
	ErrRefMismatch = errors.New("artifact: document metadata does not match the reference")
	// ErrNoJournal: the service has no journal configured, so grants
	// cannot be issued or verified.
	ErrNoJournal = errors.New("artifact: service has no journal configured")
	// ErrUnknownGrant: no grant with that id was ever issued.
	ErrUnknownGrant = errors.New("artifact: no such grant")
	// ErrGrantConsumed: the grant was already accepted; grants are
	// single-use.
	ErrGrantConsumed = errors.New("artifact: grant already accepted")
	// ErrGrantMismatch: the presented grant does not match the issued one.
	ErrGrantMismatch = errors.New("artifact: grant does not match the issued grant")
	// ErrImmutableRegion: a region outside the grant changed.
	ErrImmutableRegion = errors.New("artifact: a region outside the grant changed")
	// ErrMetadataChanged: the document's metadata block changed; metadata
	// is immutable for the document's whole life.
	ErrMetadataChanged = errors.New("artifact: document metadata changed")
	// ErrRegionSetChanged: the document's set of regions changed.
	ErrRegionSetChanged = errors.New("artifact: document region set changed")
)

// Clock is the service's time source; tests inject a fixed one.
type Clock func() time.Time

// Ref identifies one document: the work it belongs to, its kind, and its
// control-root-relative path. Path is authoritative for I/O; WorkID and
// Kind are checked against the document's metadata.
type Ref struct {
	WorkID identity.WorkID `json:"work_id"`
	Kind   Kind            `json:"kind"`
	Path   string          `json:"path"`
}

// EditGrant is the single-use permission a host or implementer presents at
// AcceptEdit. ID names the journaled grant; FreshnessToken authenticates
// the presented grant against the issued one; MetaDigest pins the
// immutable metadata block; and Before is the digest of the document's
// immutable portion (every region NOT in Regions) at issue time, which
// AcceptEdit recomputes from the edited file.
type EditGrant struct {
	ID             identity.ActionID  `json:"id"`
	ActionID       identity.ActionID  `json:"action_id,omitempty"`
	Ref            Ref                `json:"ref"`
	Owner          Owner              `json:"owner"`
	Phase          Phase              `json:"phase"`
	Regions        []Region           `json:"regions"`
	MetaDigest     fingerprint.Digest `json:"meta_digest"`
	Before         fingerprint.Digest `json:"before"`
	FreshnessToken identity.Token     `json:"freshness_token"`
}

// GrantRequest asks for edit permission on a document's regions during a
// phase. The owner is not requested — the ownership table decides it.
type GrantRequest struct {
	Ref     Ref
	Phase   Phase
	Regions []Region
	// ActionID optionally links the grant to the workflow action it
	// serves.
	ActionID identity.ActionID
}

// Snapshot is the durable result of an accepted edit or generated write:
// the document ref and the digest of its whole canonical form.
type Snapshot struct {
	Ref    Ref                `json:"ref"`
	Digest fingerprint.Digest `json:"digest"`
	At     time.Time          `json:"at"`
}

// ownership is one row of the document ownership table: who may write a
// kind's regions in a phase, and which regions. A kind/phase pair absent
// from the table is editable by nobody in that phase.
//
// The table is the spec's ownership rules, verbatim:
//
//   - Task: the host edits goal and checklist in Plan only; in Do the
//     binary checks off items whose assignments were accepted; in Done
//     the binary appends evidence.
//   - Full change: the host writes proposal.md in Open, design.md and
//     tasks.md in Design, plan.md in Build; the binary updates task
//     checkboxes in Build, generates verification.md in Verify and
//     record.md in Close; an approved implementer assignment writes ADRs
//     in Close.
//   - Fix and Tweak presets: the host writes fix.md or tweak.md plus
//     tasks.md in Open; Build/Verify/Close are as for a full change.
//   - A preset tasks.md frozen by an upgrade is editable by nobody.
type ownership struct {
	Owner   Owner
	Regions []Region
}

// ownershipTable maps a kind and phase to who may write which regions.
var ownershipTable = map[Kind]map[Phase]ownership{
	KindTaskDocument: {
		PhasePlan: {OwnerHost, []Region{RegionTaskGoal, RegionTaskChecklist}},
		PhaseDo:   {OwnerBinary, []Region{RegionTaskChecklist}},
		PhaseDone: {OwnerBinary, []Region{RegionTaskEvidence}},
	},
	KindProposal: {
		PhaseOpen: {OwnerHost, []Region{RegionWholeDocument}},
	},
	KindDesign: {
		PhaseDesign: {OwnerHost, []Region{RegionWholeDocument}},
	},
	KindTasks: {
		// Full changes author tasks.md in Design; presets author it in
		// Open. Both paths hand checkbox updates to the binary in Build.
		PhaseOpen:   {OwnerHost, []Region{RegionWholeDocument}},
		PhaseDesign: {OwnerHost, []Region{RegionWholeDocument}},
		PhaseBuild:  {OwnerBinary, []Region{RegionWholeDocument}},
	},
	KindPlan: {
		PhaseBuild: {OwnerHost, []Region{RegionWholeDocument}},
	},
	KindFix: {
		PhaseOpen: {OwnerHost, []Region{RegionWholeDocument}},
	},
	KindTweak: {
		PhaseOpen: {OwnerHost, []Region{RegionWholeDocument}},
	},
	KindVerification: {
		PhaseVerify: {OwnerBinary, []Region{RegionWholeDocument}},
	},
	KindRecord: {
		PhaseClose: {OwnerBinary, []Region{RegionWholeDocument}},
	},
	KindADR: {
		PhaseClose: {OwnerImplementer, []Region{RegionWholeDocument}},
	},
	// KindPresetTasks is deliberately absent: a frozen preset tasks.md is
	// a read-only input in every phase.
}

// Ownership reports who may write which regions of a kind in a phase. ok
// is false when nobody may.
func Ownership(k Kind, p Phase) (Owner, []Region, bool) {
	byPhase, ok := ownershipTable[k]
	if !ok {
		return "", nil, false
	}
	row, ok := byPhase[p]
	if !ok {
		return "", nil, false
	}
	return row.Owner, row.Regions, true
}

// checkRegions verifies that want is a non-empty subset of allowed, with
// no unknown, duplicated, or ungranted region.
func checkRegions(want, allowed []Region) error {
	if len(want) == 0 {
		return fmt.Errorf("artifact: no regions requested: %w", ErrRegionNotGranted)
	}
	ok := make(map[Region]bool, len(allowed))
	for _, r := range allowed {
		ok[r] = true
	}
	seen := make(map[Region]bool, len(want))
	for _, r := range want {
		if !regionKnown(r) {
			return fmt.Errorf("artifact: region %q is not a known region: %w", r, ErrRegionNotGranted)
		}
		if seen[r] {
			return fmt.Errorf("artifact: region %q requested twice: %w", r, ErrRegionNotGranted)
		}
		seen[r] = true
		if !ok[r] {
			return fmt.Errorf("artifact: region %q: %w", r, ErrRegionNotGranted)
		}
	}
	return nil
}

// grantedSet indexes a grant's regions for the digest helpers.
func grantedSet(regions []Region) map[Region]bool {
	set := make(map[Region]bool, len(regions))
	for _, r := range regions {
		set[r] = true
	}
	return set
}

// metaDigest digests a document's metadata block in its canonical rendered
// form, so any change to any metadata field changes the digest.
func metaDigest(m Metadata) (fingerprint.Digest, error) {
	b, err := RenderMetadata(m)
	if err != nil {
		return "", err
	}
	return fingerprint.Bytes("artifact-metadata", b), nil
}

// immutableBefore digests the document's immutable portion: every region
// outside granted, in canonical order, each fenced by its markers so a
// region's content can never be shifted into its neighbour undetected.
func immutableBefore(d Document, granted map[Region]bool) fingerprint.Digest {
	var buf []byte
	for _, rc := range d.Regions {
		if granted[rc.Region] {
			continue
		}
		buf = append(buf, beginMarker(rc.Region)...)
		buf = append(buf, '\n')
		buf = append(buf, rc.Content...)
		buf = append(buf, endMarker(rc.Region)...)
		buf = append(buf, '\n')
	}
	return fingerprint.Bytes("artifact-immutable-before", buf)
}

// DocumentDigest digests a document's whole canonical form. It is the
// digest a Snapshot carries and the one downstream evidence pins.
func DocumentDigest(rendered []byte) fingerprint.Digest {
	return fingerprint.Bytes("artifact-document", rendered)
}

// tokenDigest hashes a freshness token; only the hash is ever persisted.
func tokenDigest(t identity.Token) fingerprint.Digest {
	return fingerprint.Bytes("artifact-grant-token", []byte(t))
}

// tokenMatches compares a presented token against a stored hash without
// leaking the comparison through timing.
func tokenMatches(presented identity.Token, stored fingerprint.Digest) bool {
	got := tokenDigest(presented)
	return subtle.ConstantTimeCompare([]byte(got), []byte(stored)) == 1
}

// sameRegions reports whether two region lists are equal in order.
func sameRegions(a, b []Region) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
