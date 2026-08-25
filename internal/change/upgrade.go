package change

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/identity"
)

// ErrNotAPreset reports an upgrade attempted on a change that is already
// Full.
var ErrNotAPreset = errors.New("change: only a preset can be upgraded")

// UpgradeDecision is the human choice that authorizes an upgrade.
type UpgradeDecision struct {
	// DecisionID is the decision action that carried the choice.
	DecisionID identity.ActionID
	// Rationale is why the preset outgrew itself. It goes into the
	// proposal, because the reason a change became Full is exactly the
	// kind of thing a future maintainer asks about.
	Rationale string
}

// UpgradePreset turns a Fix or Tweak into a Full change.
//
// The spec is precise about what survives, and each clause exists to keep
// history rather than tidy it away:
//
//   - fix.md or tweak.md is KEPT as a read-only preset input. It is what
//     the human originally confirmed, and deleting it would erase the
//     record of what the change was before it grew.
//   - The existing tasks.md is RENAMED to preset-tasks.md, frozen. It is
//     editable by nobody (the ownership table has no row for it), so the
//     new Design writes a fresh tasks.md rather than editing a list that
//     was scoped to a smaller change.
//   - proposal.md is created FROM the confirmed intent, so Design starts
//     from what the human agreed to rather than from a blank page.
//   - The change rewinds to Design, and human design approval is required
//     before implementation continues.
//   - The immutable work baseline is PRESERVED. Upgrading is not a way to
//     reset the ruler.
func (e *Engine) UpgradePreset(ctx context.Context, st State, in UpgradeDecision) (State, error) {
	if !st.Path.Preset() {
		return State{}, fmt.Errorf("change: %s is already full: %w", st.Name, ErrNotAPreset)
	}
	if strings.TrimSpace(in.Rationale) == "" {
		return State{}, fmt.Errorf("change: upgrading %s needs a rationale", st.Name)
	}
	inputKind, err := st.Path.InputKind()
	if err != nil {
		return State{}, err
	}
	inputPath, err := st.DocumentPath(inputKind)
	if err != nil {
		return State{}, err
	}
	intent, err := e.documentBody(ctx, st, inputKind, inputPath)
	if err != nil {
		return State{}, err
	}

	if err := e.freezePresetTasks(ctx, st); err != nil {
		return State{}, err
	}
	if err := e.createProposal(ctx, st, inputKind, intent, in.Rationale); err != nil {
		return State{}, err
	}

	upgraded := st
	upgraded.UpgradedFrom = st.Path
	upgraded.Path = PathFull
	upgraded.Step = string(StepDesignDraft)
	// A fresh generation: every preset-only assignment, check, and report
	// belongs to a change that no longer exists.
	upgraded.Generation++
	upgraded.UpdatedAt = e.now().UTC()
	if err := e.invalidateOpenActions(ctx, st); err != nil {
		return State{}, err
	}
	if err := e.rebaselineDocuments(ctx, &upgraded); err != nil {
		return State{}, err
	}
	// Preset-only downstream evidence is invalidated: it was taken against
	// a change with a different shape.
	if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
		return State{}, err
	}
	upgraded.Baseline.Verification = ""
	// The work baseline is deliberately carried over untouched.
	upgraded.Baseline.Work = st.Baseline.Work
	if err := e.saveState(ctx, upgraded); err != nil {
		return State{}, err
	}
	return upgraded, nil
}

// freezePresetTasks copies the preset's tasks.md into preset-tasks.md and
// clears the original so Design writes a fresh list.
//
// It is a copy rather than a move because a document's metadata block
// carries its KIND and metadata is immutable for the document's whole
// life: the frozen input is a preset-tasks document and the new list is a
// tasks document, and they cannot be the same file.
func (e *Engine) freezePresetTasks(ctx context.Context, st State) error {
	tasksPath, err := st.DocumentPath(artifact.KindTasks)
	if err != nil {
		return err
	}
	ref := artifact.Ref{WorkID: st.WorkID, Kind: artifact.KindTasks, Path: tasksPath}
	doc, err := e.artifacts.Read(ctx, ref)
	if errors.Is(err, artifact.ErrArtifactMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	body := doc.Region(artifact.RegionWholeDocument)

	frozenPath, err := st.DocumentPath(artifact.KindPresetTasks)
	if err != nil {
		return err
	}
	frozen := artifact.NewDocument(artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: st.WorkID,
		Name: st.Name, Kind: artifact.KindPresetTasks,
	})
	frozen.Regions = []artifact.RegionContent{{
		Region:  artifact.RegionWholeDocument,
		Content: append([]byte("<!-- Frozen preset task list; read-only input to the upgraded change. -->\n\n"), body...),
	}}
	if _, err := e.artifacts.Create(ctx, frozenPath, frozen.Metadata); err != nil {
		return err
	}
	if err := artifact.WriteRaw(e.artifacts, artifact.Ref{
		WorkID: st.WorkID, Kind: artifact.KindPresetTasks, Path: frozenPath,
	}, frozen); err != nil {
		return err
	}
	// Empty the live list. Design writes a new one; leaving the preset's
	// list in place would let a Build partition work that was scoped to a
	// change that no longer exists.
	emptied := doc
	emptied.Regions = []artifact.RegionContent{{
		Region:  artifact.RegionWholeDocument,
		Content: []byte("<!-- Superseded by the upgrade; Design writes the new task list. -->\n"),
	}}
	return artifact.WriteRaw(e.artifacts, ref, emptied)
}

// createProposal writes proposal.md from the confirmed preset intent and
// the reason the preset outgrew itself.
func (e *Engine) createProposal(ctx context.Context, st State, inputKind artifact.Kind, intent, rationale string) error {
	proposalPath, err := st.DocumentPath(artifact.KindProposal)
	if err != nil {
		return err
	}
	meta := artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: st.WorkID,
		Name: st.Name, Kind: artifact.KindProposal,
	}
	if _, err := e.artifacts.Create(ctx, proposalPath, meta); err != nil {
		return err
	}
	inputPath, err := st.DocumentPath(inputKind)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("## Confirmed intent\n\n")
	b.WriteString(strings.TrimSpace(intent))
	b.WriteString("\n\n## Why this became a full change\n\n")
	b.WriteString(strings.TrimSpace(rationale))
	fmt.Fprintf(&b, "\n\n## Preset inputs\n\n- %s (read-only)\n- %s (frozen task list)\n",
		inputPath, presetTasksPath(st))

	doc := artifact.NewDocument(meta)
	doc.Regions = []artifact.RegionContent{{
		Region: artifact.RegionWholeDocument, Content: []byte(b.String()),
	}}
	return artifact.WriteRaw(e.artifacts, artifact.Ref{
		WorkID: st.WorkID, Kind: artifact.KindProposal, Path: proposalPath,
	}, doc)
}

// presetTasksPath renders the frozen task list's path for the proposal.
func presetTasksPath(st State) string {
	path, err := st.DocumentPath(artifact.KindPresetTasks)
	if err != nil {
		return "preset-tasks.md"
	}
	return path
}

// documentBody reads one document's whole content.
func (e *Engine) documentBody(ctx context.Context, st State, kind artifact.Kind, path string) (string, error) {
	doc, err := e.artifacts.Read(ctx, artifact.Ref{WorkID: st.WorkID, Kind: kind, Path: path})
	if err != nil {
		return "", err
	}
	return string(doc.Region(artifact.RegionWholeDocument)), nil
}
