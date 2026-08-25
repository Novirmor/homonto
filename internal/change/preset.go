package change

import (
	"context"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// stepPresetDraft hands the host edit grants on the preset's input
// document and its task list. Both are written in Open: a preset's whole
// claim is that it already knows what it is doing, so it says so up front
// rather than deriving it through a design.
func (e *Engine) stepPresetDraft(ctx context.Context, st State, step Step) (State, bool, error) {
	inputKind, err := st.Path.InputKind()
	if err != nil {
		return st, false, err
	}
	what := "record the intent and the exact behavior delta"
	if st.Path == PathFix {
		what = "record the reproduction, the expected and actual behavior, and the root cause"
	}
	return e.stepDraft(ctx, st, step, EventPresetDrafted,
		[]artifact.Kind{inputKind, artifact.KindTasks}, what)
}

// stepReproduce gates a Fix on evidence that the defect exists.
//
// A failing automated test or a reproducible command is required before
// implementation. When reproduction is not reasonably automatable, the
// reason is recorded in fix.md and a human must approve the exception —
// which is a decision, not a formality: "we could not reproduce it" is
// exactly the condition under which a fix is most likely to fix nothing.
func (e *Engine) stepReproduce(ctx context.Context, st State, step Step) (State, bool, error) {
	if st.Path != PathFix {
		return st, false, fmt.Errorf("change: only a fix reproduces a defect, not a %s", st.Path)
	}
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		reproduction, err := e.reproduction(ctx, st)
		if err != nil {
			return st, false, err
		}
		if err := reproduction.Validate(); err == nil {
			// The fix document carries a reproduction; nothing to ask.
			return e.advance(ctx, st, step, EventReproductionRecorded)
		}
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		path, err := st.DocumentPath(artifact.KindFix)
		if err != nil {
			return st, false, err
		}
		spec, err := e.decisionSpec(st, step, control,
			reproductionExceptionSchema(path, reproduction.Reason),
			"the fix records no failing reproduction")
		if err != nil {
			return st, false, err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	sub, found, err := e.assignments.Decision(ctx, issued[0].ID)
	if err != nil {
		return st, false, err
	}
	if !found {
		return st, false, fmt.Errorf("change: decision %s is answered but carries no choice", issued[0].ID)
	}
	if sub.Choice != "accept" {
		// The human sent it back: the fix document must gain a
		// reproduction before the defect is implemented against.
		st.Generation++
		next, err := Advance(st.Path, step, EventAbandon)
		if sub.Choice == "reproduce" {
			next, err = StepPresetOpenDraft, nil
		}
		if err != nil {
			return st, false, err
		}
		st, err = e.moveTo(ctx, st, next)
		return st, err == nil, err
	}
	return e.advance(ctx, st, step, EventReproductionRecorded)
}

// Reproduction is what a fix.md must carry before implementation starts.
type Reproduction struct {
	// Command is a reproducible command, or Test is a failing automated
	// test. Either satisfies the gate.
	Command string
	Test    string
	// Reason records why neither is reasonably automatable. It is what the
	// human approves an exception against.
	Reason string
}

// Validate reports whether the reproduction satisfies the gate on its own.
func (r Reproduction) Validate() error {
	if strings.TrimSpace(r.Command) != "" || strings.TrimSpace(r.Test) != "" {
		return nil
	}
	return fmt.Errorf("change: a fix needs a failing automated test or a reproducible command")
}

// reproductionMarkers are the fix.md headings the reproduction is read
// from. They are literal because the document is written by a host, and
// asking a host to fill in a named section is a clearer contract than
// asking Homonto to infer intent from prose.
const (
	markerCommand = "reproduce:"
	markerTest    = "failing test:"
	markerReason  = "not automatable:"
)

// reproduction reads the reproduction out of the fix document.
func (e *Engine) reproduction(ctx context.Context, st State) (Reproduction, error) {
	path, err := st.DocumentPath(artifact.KindFix)
	if err != nil {
		return Reproduction{}, err
	}
	doc, err := e.artifacts.Read(ctx, artifact.Ref{
		WorkID: st.WorkID, Kind: artifact.KindFix, Path: path,
	})
	if err != nil {
		return Reproduction{}, err
	}
	return ParseReproduction(doc.Region(artifact.RegionWholeDocument)), nil
}

// ParseReproduction extracts the reproduction fields from a fix document.
func ParseReproduction(body []byte) Reproduction {
	var r Reproduction
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*# "))
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, markerCommand):
			r.Command = strings.TrimSpace(trimmed[len(markerCommand):])
		case strings.HasPrefix(lower, markerTest):
			r.Test = strings.TrimSpace(trimmed[len(markerTest):])
		case strings.HasPrefix(lower, markerReason):
			r.Reason = strings.TrimSpace(trimmed[len(markerReason):])
		}
	}
	return r
}

// reproductionExceptionSchema asks a human to accept a fix with no
// reproduction. Accepting requires a rationale: an exception nobody
// explained is indistinguishable from having skipped the step.
func reproductionExceptionSchema(path, reason string) decision.Schema {
	stated := reason
	if strings.TrimSpace(stated) == "" {
		stated = "no reason was recorded"
	}
	return decision.Schema{
		Kind: decision.KindReproductionException,
		Prompt: fmt.Sprintf(
			"%s records no failing test and no reproducible command.\n\nStated reason: %s\n\n"+
				"A fix without a reproduction is a fix that may fix nothing. Approve the "+
				"exception, or send it back for a reproduction.", path, stated),
		Choices: []decision.Choice{
			{Value: "reproduce", Label: "Send it back for a reproduction"},
			{Value: "accept", Label: "Accept the exception", RequiresRationale: true},
		},
	}
}

// stepPresetScope runs the preset scope assessment against the immutable
// work baseline and pauses for a human when anything fires.
//
// Nothing here upgrades. The human continues the preset with the broader
// scope recorded, or upgrades to Full — and both of those are answers to a
// question this step only poses.
func (e *Engine) stepPresetScope(ctx context.Context, st State, step Step) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		observed, err := e.observedSignals(ctx, st.WorkID)
		if err != nil {
			return st, false, err
		}
		assessment, err := e.PresetScope(ctx, st, observed)
		if err != nil {
			return st, false, err
		}
		if !assessment.Pause {
			return e.advance(ctx, st, step, EventScopeClear)
		}
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		spec, err := e.decisionSpec(st, step, control,
			presetTripwireSchema(st, assessment), "the preset scope assessment fired")
		if err != nil {
			return st, false, err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	sub, found, err := e.assignments.Decision(ctx, issued[0].ID)
	if err != nil {
		return st, false, err
	}
	if !found {
		return st, false, fmt.Errorf("change: decision %s is answered but carries no choice", issued[0].ID)
	}
	switch sub.Choice {
	case "continue":
		return e.advance(ctx, st, step, EventScopeContinued)
	case "upgrade":
		upgraded, err := e.UpgradePreset(ctx, st, UpgradeDecision{
			DecisionID: issued[0].ID, Rationale: sub.Rationale,
		})
		if err != nil {
			return st, false, err
		}
		return upgraded, true, nil
	}
	return st, false, fmt.Errorf("change: decision %s carries unrecognized choice %q", issued[0].ID, sub.Choice)
}

// presetTripwireSchema asks the human what to do about a preset that has
// outgrown itself. Continuing requires a rationale — the broader scope is
// then recorded, which is the point — and so does upgrading, because both
// are decisions a future maintainer may reasonably question.
func presetTripwireSchema(st State, a pathclass.Assessment) decision.Schema {
	var b strings.Builder
	fmt.Fprintf(&b, "The %s preset %q has tripped its scope assessment.\n\n", st.Path, st.Name)
	for _, e := range a.Evidence {
		fmt.Fprintf(&b, "- %s\n", e)
	}
	b.WriteString("\nContinue the preset with the broader scope recorded, or upgrade to Full.")
	return decision.Schema{
		Kind:   decision.KindPresetTripwire,
		Prompt: b.String(),
		Choices: []decision.Choice{
			{Value: "upgrade", Label: "Upgrade to the full path", RequiresRationale: true},
			{Value: "continue", Label: "Continue the preset with the broader scope recorded",
				RequiresRationale: true},
		},
	}
}

// observedSignals reads the preset scope signals out of the change's
// answered reports. They are carried as finding ids matching the signal
// vocabulary, so a host reports them explicitly rather than having Homonto
// guess from prose.
func (e *Engine) observedSignals(ctx context.Context, workID identity.WorkID) ([]pathclass.Signal, error) {
	actions, err := e.assignments.Actions(ctx, workID)
	if err != nil {
		return nil, err
	}
	seen := map[pathclass.Signal]bool{}
	var out []pathclass.Signal
	for _, act := range actions {
		if act.Kind != protocol.KindAssignment || act.State != assignment.StateSubmitted {
			continue
		}
		sub, found, err := e.assignments.Report(ctx, act.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		wire := act.Spec
		wire.FreshnessToken = e.assignments.Token(act.ID)
		report, err := protocol.DecodeReport(wire, sub.Report)
		if err != nil {
			return nil, err
		}
		for _, f := range findingsOf(report) {
			signal := pathclass.Signal(f.ID)
			if !signal.Semantic() || seen[signal] {
				continue
			}
			seen[signal] = true
			out = append(out, signal)
		}
	}
	return pathclass.SortSignals(out), nil
}
