package ontocli

import (
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func gateIDs(gs []pendingGate) []string {
	var ids []string
	for _, g := range gs {
		ids = append(ids, g.ID)
	}
	return ids
}

func TestPendingGates_ByPhaseAndState(t *testing.T) {
	// full open with no token → the proposal-approved judgment gate.
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "open", Workflow: "full"})); strings.Join(ids, ",") != "proposal-approved" {
		t.Errorf("full open gates = %v, want [proposal-approved]", ids)
	}
	// a preset open never gates proposal approval.
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "open", Workflow: "fix"})); len(ids) != 0 {
		t.Errorf("fix open gates = %v, want none", ids)
	}
	// full design with nothing recorded → approach first, then isolation.
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "design", Workflow: "full"})); strings.Join(ids, ",") != "approach-confirmed,isolation" {
		t.Errorf("design gates = %v, want [approach-confirmed isolation]", ids)
	}
	// design with both answered → no gate.
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "design", Workflow: "full", Isolation: "branch", ApproachConfirmed: "2026-07-22 ok"})); len(ids) != 0 {
		t.Errorf("design/answered gates = %v, want none", ids)
	}
	// a preset design gates isolation only.
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "design", Workflow: "tweak"})); strings.Join(ids, ",") != "isolation" {
		t.Errorf("tweak design gates = %v, want [isolation]", ids)
	}
	// build with nothing recorded → build-mode + tdd-mode.
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "build"})); strings.Join(ids, ",") != "build-mode,tdd-mode" {
		t.Errorf("build gates = %v, want [build-mode tdd-mode]", ids)
	}
	// full close missing everything → confirmation first, then merged, guides, integration.
	full := ontostate.State{Phase: "close", Workflow: "full"}
	if ids := gateIDs(pendingGates("c", full)); strings.Join(ids, ",") != "close-confirmed,close-merged,guides,integration" {
		t.Errorf("full close gates = %v, want [close-confirmed close-merged guides integration]", ids)
	}
	// a tweak close does not gate guides but does gate confirmation.
	tweak := ontostate.State{Phase: "close", Workflow: "tweak", Close: ontostate.Close{Merged: true}, Integration: "merge"}
	if ids := gateIDs(pendingGates("c", tweak)); strings.Join(ids, ",") != "close-confirmed" {
		t.Errorf("resolved tweak close gates = %v, want [close-confirmed]", ids)
	}
	if ids := gateIDs(pendingGates("c", ontostate.State{Phase: "close", Workflow: "tweak", Close: ontostate.Close{Merged: true}, Integration: "merge", CloseConfirmed: "2026-07-22 ok"})); len(ids) != 0 {
		t.Errorf("fully answered tweak close gates = %v, want none", ids)
	}
}

func TestGateCommand_JSONAndHuman(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10",
		Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true}, Guides: "updated",
		Integration: "", // the only pending gate
	})

	// JSON carries the structured schema a dialog renders.
	jout, err := runOnto(t, "gate", "demo", "--dir", dir, "--json")
	if err != nil {
		t.Fatalf("gate --json: %v", err)
	}
	for _, want := range []string{`"id": "integration"`, `"set_command"`, `"merge"`, `"pr"`} {
		if !strings.Contains(jout, want) {
			t.Errorf("gate --json missing %q:\n%s", want, jout)
		}
	}
	// Human form names the set command.
	hout, err := runOnto(t, "gate", "demo", "--dir", dir)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !strings.Contains(hout, "onto set integration demo") {
		t.Errorf("gate human output missing set command:\n%s", hout)
	}
}
