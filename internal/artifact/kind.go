// Package artifact owns the on-disk workflow documents: their kinds, the
// canonical metadata block and region markers every document carries, the
// edit-grant machinery that lets a host edit only the regions a phase
// grants it, and the binary-owned generated writes (checkboxes, evidence,
// verification, record). Filesystem access goes through securefs; the
// durable record of grants goes through the store journal.
//
// # Document layout
//
// Every artifact document is a Markdown file whose first line is the
// metadata comment and whose editable content is partitioned into named
// regions by begin/end marker comments:
//
//	<!-- homonto: {"schema":1,"work_id":"…","name":"…","kind":"task"} -->
//	<!-- homonto:begin task-goal -->
//	…goal text…
//	<!-- homonto:end task-goal -->
//	<!-- homonto:begin task-checklist -->
//	- [ ] item
//	<!-- homonto:end task-checklist -->
//
// Task documents use the three task regions; every other kind is a single
// whole-document region with no markers. Outside the metadata line, the
// markers, and blank separator lines, nothing may appear — the layout is
// byte-precise so a grant's before/after digests partition the file
// exactly, and any stray byte is tampering, not formatting.
package artifact

// Kind identifies one workflow document type. The spelling is persisted in
// each document's metadata block and read back by archive lookups, so it
// is wire: changing a value orphans every existing document.
type Kind string

const (
	// KindTaskDocument: a task's single file (goal, checklist, evidence).
	KindTaskDocument Kind = "task"
	// KindProposal: a full change's proposal.md (host-written in Open).
	KindProposal Kind = "proposal"
	// KindDesign: a full change's design.md (host-written in Design).
	KindDesign Kind = "design"
	// KindTasks: a change's tasks.md — host-written in Design (full) or
	// Open (presets), checkbox-updated by the binary in Build.
	KindTasks Kind = "tasks"
	// KindPresetTasks: a preset tasks.md frozen by upgrade to full — a
	// read-only input, editable by no one.
	KindPresetTasks Kind = "preset-tasks"
	// KindPlan: a full change's plan.md (host-written in Build).
	KindPlan Kind = "plan"
	// KindFix: a fix preset's fix.md (host-written in Open).
	KindFix Kind = "fix"
	// KindTweak: a tweak preset's tweak.md (host-written in Open).
	KindTweak Kind = "tweak"
	// KindVerification: the binary-generated verification.md (Verify).
	KindVerification Kind = "verification"
	// KindRecord: the binary-generated record.md (Close).
	KindRecord Kind = "record"
	// KindADR: an architecture decision record written by an implementer
	// assignment in Close. ADRs are plain repository documents: they carry
	// no metadata block and are handled whole-file.
	KindADR Kind = "adr"
)

// known reports whether k is one of the persisted kinds.
func (k Kind) known() bool {
	switch k {
	case KindTaskDocument, KindProposal, KindDesign, KindTasks,
		KindPresetTasks, KindPlan, KindFix, KindTweak,
		KindVerification, KindRecord, KindADR:
		return true
	}
	return false
}

// Region names one editable partition of a document. Task documents carry
// three explicit regions; every other kind is one implicit whole-document
// region.
type Region string

const (
	// RegionTaskGoal: the task's goal statement.
	RegionTaskGoal Region = "task-goal"
	// RegionTaskChecklist: the task's checkbox list.
	RegionTaskChecklist Region = "task-checklist"
	// RegionTaskEvidence: the binary-appended completion evidence.
	RegionTaskEvidence Region = "task-evidence"
	// RegionWholeDocument: the entire content of a non-task document.
	RegionWholeDocument Region = "whole-document"
)

// taskRegions is the canonical region order of a task document.
var taskRegions = []Region{RegionTaskGoal, RegionTaskChecklist, RegionTaskEvidence}

// regionKnown reports whether r is one of the named regions.
func regionKnown(r Region) bool {
	switch r {
	case RegionTaskGoal, RegionTaskChecklist, RegionTaskEvidence, RegionWholeDocument:
		return true
	}
	return false
}

// regionsOf returns the canonical region set a kind's documents carry.
func regionsOf(k Kind) []Region {
	if k == KindTaskDocument {
		return taskRegions
	}
	return []Region{RegionWholeDocument}
}

// taskRegion reports whether r is one of the explicit task regions.
func taskRegion(r Region) bool {
	return r == RegionTaskGoal || r == RegionTaskChecklist || r == RegionTaskEvidence
}

// Owner names who may edit a region in the phase that owns it. The binary
// is Homonto itself; the host is the human's agent session; the
// implementer is a write-scoped implementer assignment.
type Owner string

const (
	// OwnerBinary: only Homonto writes (via Service.WriteGenerated).
	OwnerBinary Owner = "binary"
	// OwnerHost: the host agent writes through an edit grant.
	OwnerHost Owner = "host"
	// OwnerImplementer: a write-scoped implementer assignment writes
	// through an edit grant.
	OwnerImplementer Owner = "implementer"
)

// Phase names a workflow phase for the ownership table. Task uses
// plan/do/done; every change path uses open/design/build/verify/close.
type Phase string

const (
	// PhasePlan: task planning.
	PhasePlan Phase = "plan"
	// PhaseDo: task implementation.
	PhaseDo Phase = "do"
	// PhaseDone: task completion.
	PhaseDone Phase = "done"
	// PhaseOpen: change opening (and preset fix/tweak opening).
	PhaseOpen Phase = "open"
	// PhaseDesign: full-change design.
	PhaseDesign Phase = "design"
	// PhaseBuild: change implementation.
	PhaseBuild Phase = "build"
	// PhaseVerify: change verification.
	PhaseVerify Phase = "verify"
	// PhaseClose: change close.
	PhaseClose Phase = "close"
)
