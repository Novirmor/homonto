package change

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/verify"
)

// checkSet builds a verification set with a single check of the given
// outcome.
func checkSet(t *testing.T, repo identity.RepositoryID, outcome verify.Outcome) verify.Set {
	t.Helper()
	spec := verify.Spec{Name: "unit", Command: []string{"/bin/true"}}
	pin, err := spec.Digest()
	if err != nil {
		t.Fatalf("Spec.Digest: %v", err)
	}
	exit := 0
	if outcome != verify.OutcomePassed {
		exit = 1
	}
	return verify.Set{
		Inputs: verify.Inputs{Repository: repo, Config: fingerprint.Bytes("checks", []byte("v1"))},
		At:     testNow,
		Results: []verify.Result{{
			Spec: spec, SpecPin: pin, Outcome: outcome, ExitCode: exit, StartedAt: testNow,
		}},
	}
}

// enableWork lets the scripted environment hand out implementation units.
func (h *harness) enableWork(t *testing.T) {
	t.Helper()
	h.env.checks = []verify.Set{checkSet(t, h.env.control.ID, verify.OutcomePassed)}
	h.env.partition = func(items []artifact.Item) []Unit {
		if len(items) == 0 {
			return nil
		}
		out := make([]Unit, 0, len(items))
		for _, it := range items {
			out = append(out, Unit{
				Label:  strings.ReplaceAll(it.Text, " ", "-"),
				Member: h.env.control,
				Items:  []int{it.Index},
				Scope:  []string{"src"},
				Prompt: "Implement: " + it.Text,
			})
		}
		return out
	}
}

// confirm walks a candidate to its gate and confirms the given path.
func (h *harness) confirm(t *testing.T, name, request string, path Path, signals ...pathclass.Signal) State {
	t.Helper()
	pre, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{Name: name, Request: request})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	h.assess(t, pre, signals...)
	rationale := ""
	after, err := h.engine.Preflight(t.Context(), pre.WorkID)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if after.Suggestion.Path != path {
		rationale = "the human knows better"
	}
	st, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: pre.WorkID, Path: path, Rationale: rationale,
	})
	if err != nil {
		t.Fatalf("ConfirmPreflight: %v", err)
	}
	return st
}

// next asks the engine what to do and validates the payload.
func (h *harness) next(t *testing.T, id identity.WorkID) protocol.NextResponse {
	t.Helper()
	resp, err := h.engine.Next(t.Context(), id)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("Next returned an invalid response: %v", err)
	}
	return resp
}

// implementerReport is the canned answer for a writable assignment.
func implementerReport(act protocol.Action) protocol.ImplementerReport {
	return protocol.ImplementerReport{
		Material: protocol.Material{
			Kind: protocol.MaterialSnapshotPatch, PatchManifest: []string{"src/a.go"},
			Content: fingerprint.Bytes("material", []byte(act.ID)),
		},
		ChangedPaths: []string{"src/a.go"},
	}
}

// answerChange dispatches one Change action to its canned response.
func (h *harness) answerChange(t *testing.T, act protocol.Action, blocking []protocol.Finding) {
	t.Helper()
	switch act.Kind {
	case protocol.KindEdit:
		h.edit(t, act)
		return
	case protocol.KindDecision:
		h.decideChange(t, act)
		return
	}
	var payload any
	switch act.Role {
	case protocol.RoleExplorer:
		payload = protocol.ExplorerReport{Facts: []string{"the catalog is in src"}}
	case protocol.RoleSkeptic:
		payload = protocol.SkepticReport{Assumptions: []string{"one module is affected"}, Findings: blocking}
	case protocol.RoleReviewer:
		payload = protocol.ReviewerReport{Acceptance: []string{"the tasks are covered"}, Findings: blocking}
	case protocol.RoleImplementer:
		payload = implementerReport(act)
	default:
		t.Fatalf("no canned report for role %q", act.Role)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if _, err := h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        act.ID, FreshnessToken: act.FreshnessToken,
		Role: act.Role, Session: session(t), Report: raw,
	}); err != nil {
		t.Fatalf("SubmitReport(%s): %v", act.Role, err)
	}
}

// edit performs the host's document edit and accepts it.
func (h *harness) edit(t *testing.T, act protocol.Action) {
	t.Helper()
	abs := filepath.Join(h.root, filepath.FromSlash(act.Edit.Document))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", act.Edit.Document, err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", act.Edit.Document, err)
	}
	body := "## " + act.Edit.Kind + "\n\nWritten by the host.\n"
	if artifact.Kind(act.Edit.Kind) == artifact.KindTasks {
		body = "## Tasks\n\n- [ ] replace the storage layer\n"
	}
	if artifact.Kind(act.Edit.Kind) == artifact.KindFix {
		body = "## Fix\n\nreproduce: go test ./internal/catalog\n\nRoot cause: the cache key.\n"
	}
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionWholeDocument {
			doc.Regions[i].Content = []byte(body)
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := h.engine.AcceptEdit(t.Context(), act.ID, act.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
}

// decideChange answers a Change decision gate with its first non-rejecting
// choice.
func (h *harness) decideChange(t *testing.T, act protocol.Action) {
	t.Helper()
	choice := "approve"
	switch act.Decision.Kind {
	case protocol.DecisionKind(decision.KindPresetTripwire):
		choice = "continue"
	case protocol.DecisionKind(decision.KindReproductionException):
		choice = "accept"
	case protocol.DecisionKind(decision.KindRepairLimit):
		choice = "continue"
	}
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: act.ID, FreshnessToken: act.FreshnessToken,
		Choice: choice, Rationale: "fixture decision",
	}); err != nil {
		t.Fatalf("Decide(%s): %v", act.Decision.Kind, err)
	}
}

// driveChange runs a change until it finishes or stop says so.
func (h *harness) driveChange(t *testing.T, id identity.WorkID, stop func(protocol.NextResponse) bool) State {
	t.Helper()
	for i := 0; i < 60; i++ {
		resp := h.next(t, id)
		if resp.State == protocol.NextComplete {
			break
		}
		if stop != nil && stop(resp) {
			break
		}
		for _, act := range resp.Actions {
			h.answerChange(t, act, nil)
		}
		st, err := h.engine.State(t.Context(), id)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		if terminalStep(st.Path, st.Step) {
			break
		}
	}
	st, err := h.engine.State(t.Context(), id)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	return st
}

// TestFullChangeReachesTheArchive walks a Full change end to end: two
// approvals, four roles, implementation, integration, checks, review,
// verification.md, and one archived record.
func TestFullChangeReachesTheArchive(t *testing.T) {
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "rework-catalog", "Replace the catalog storage layer.",
		PathFull, pathclass.SignalArchitecture)

	steps := map[Step]bool{}
	roles := map[protocol.Role]bool{}
	for i := 0; i < 60; i++ {
		resp := h.next(t, st.WorkID)
		if resp.State == protocol.NextComplete {
			break
		}
		cur, err := h.engine.State(t.Context(), st.WorkID)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		steps[Step(cur.Step)] = true
		for _, act := range resp.Actions {
			if act.Role != "" {
				roles[act.Role] = true
			}
			h.answerChange(t, act, nil)
		}
	}
	final, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if Step(final.Step) != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}

	// Every step that waits on a host was visited, including both
	// approvals. The steps Homonto runs alone — the checks, the
	// verification record, the finalize — are traversed inside a single
	// Next call and so are never observed here; the documents they produce
	// are asserted below instead.
	for _, want := range []Step{
		StepOpenExplore, StepOpenChallenge, StepOpenDraft, StepOpenApprove,
		StepDesignDraft, StepDesignChallenge, StepDesignApprove,
		StepBuildPlan, StepBuildImplement, StepBuildIntegrate, StepVerifyReview,
	} {
		if !steps[want] {
			t.Errorf("the change never visited %s", want)
		}
	}
	for _, role := range []protocol.Role{
		protocol.RoleExplorer, protocol.RoleImplementer,
		protocol.RoleReviewer, protocol.RoleSkeptic,
	} {
		if !roles[role] {
			t.Errorf("the %s role was never assigned", role)
		}
	}

	// The change directory left the active tree for the archive, carrying
	// every document the spec requires of a Full change.
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(archive.ChangesArchiveDir)))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive holds %d entries, want 1", len(entries))
	}
	dir := filepath.Join(h.root, filepath.FromSlash(archive.ChangesArchiveDir), entries[0].Name())
	for _, file := range []string{
		"proposal.md", "design.md", "tasks.md", "plan.md", "verification.md", "record.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("the archived change has no %s: %v", file, err)
		}
	}
	record, err := os.ReadFile(filepath.Join(dir, "record.md"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	for _, want := range []string{
		"## Outcome", "## Verification", "## Accepted deviations",
		"nothing was merged into any member's own branch",
	} {
		if !strings.Contains(string(record), want) {
			t.Errorf("the record does not carry %q:\n%s", want, record)
		}
	}
	verification, err := os.ReadFile(filepath.Join(dir, "verification.md"))
	if err != nil {
		t.Fatalf("read verification: %v", err)
	}
	for _, want := range []string{"## Acceptance", "## Commands", "## Findings", "## Repairs", "## Residual risks"} {
		if !strings.Contains(string(verification), want) {
			t.Errorf("verification.md does not carry %q:\n%s", want, verification)
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(artifact.ChangesDir), "rework-catalog")); err == nil {
		t.Fatal("the change directory is still in the active tree")
	}
}

// TestRejectedScopeReturnsToTheDraft proves an approval gate is a real
// gate: saying no sends the proposal back rather than continuing.
func TestRejectedScopeReturnsToTheDraft(t *testing.T) {
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "rework-catalog", "Replace the catalog storage layer.",
		PathFull, pathclass.SignalArchitecture)

	var gate protocol.Action
	h.driveChange(t, st.WorkID, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Decision != nil &&
				act.Decision.Kind == protocol.DecisionKind(decision.KindApproveScope) {
				gate = act
				return true
			}
		}
		return false
	})
	if gate.ID == "" {
		t.Fatal("the change never reached the scope approval")
	}
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken,
		Choice: "revise", Rationale: "the scope is too broad",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, err := h.engine.Next(t.Context(), st.WorkID); err != nil {
		t.Fatalf("Next: %v", err)
	}
	after, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if Step(after.Step) != StepOpenDraft && Step(after.Step) != StepOpenApprove {
		t.Fatalf("step = %s, want the proposal back in drafting", after.Step)
	}
	if after.Generation <= st.Generation {
		t.Fatal("a rejection did not open a new generation, so the gate could not be asked again")
	}
}

// TestBlockingFindingSendsAFullChangeToRepair proves the Verify gate.
func TestBlockingFindingSendsAFullChangeToRepair(t *testing.T) {
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "rework-catalog", "Replace the catalog storage layer.",
		PathFull, pathclass.SignalArchitecture)

	h.driveChange(t, st.WorkID, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleReviewer {
				return true
			}
		}
		return false
	})
	cur, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if Step(cur.Step) != StepVerifyReview {
		t.Fatalf("step = %s, want verify_review", cur.Step)
	}
	blocking := []protocol.Finding{{
		ID: "F-1", Severity: protocol.SeverityCritical,
		Summary: "the migration drops rows", Evidence: []string{"src/a.go:42"},
		Recommendation: "keep the old table until the backfill completes",
	}}
	resp := h.next(t, st.WorkID)
	for _, act := range resp.Actions {
		if act.Role == protocol.RoleReviewer {
			h.answerChange(t, act, blocking)
			continue
		}
		h.answerChange(t, act, nil)
	}
	if _, err := h.engine.Next(t.Context(), st.WorkID); err != nil {
		t.Fatalf("Next: %v", err)
	}
	after, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if Step(after.Step) == StepVerifyRecord || Step(after.Step) == StepArchived {
		t.Fatalf("step = %s; a critical finding must block completion", after.Step)
	}
}

// TestOpenFindingsAreNotGatingFindings proves a scope observation raised
// during Open does not gate the change: it is an observation about the
// change's shape, not a defect in a result that does not exist yet.
func TestOpenFindingsAreNotGatingFindings(t *testing.T) {
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "rework-catalog", "Replace the catalog storage layer.",
		PathFull, pathclass.SignalArchitecture)

	// The Open skeptic raises a critical finding.
	blocking := []protocol.Finding{{
		ID: "S-1", Severity: protocol.SeverityCritical,
		Summary: "this looks much larger than stated", Evidence: []string{"docs/x.md"},
		Recommendation: "split it",
	}}
	for i := 0; i < 6; i++ {
		resp := h.next(t, st.WorkID)
		done := false
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleSkeptic {
				h.answerChange(t, act, blocking)
				done = true
				continue
			}
			h.answerChange(t, act, nil)
		}
		if done {
			break
		}
	}
	blockers, err := h.engine.findings.Blockers(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("an Open finding was recorded as a gating finding: %+v", blockers)
	}
}

// TestAbandonStopsTheChangeAndClosesOpenActions proves abandoning stops
// the workflow without destroying anything.
func TestAbandonStopsTheChangeAndClosesOpenActions(t *testing.T) {
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "rework-catalog", "Replace the catalog storage layer.",
		PathFull, pathclass.SignalArchitecture)
	resp := h.next(t, st.WorkID)
	open := resp.Actions[0]

	final, err := h.engine.Abandon(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if Step(final.Step) != StepAbandoned {
		t.Fatalf("step = %s, want abandoned", final.Step)
	}
	if _, err := h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        open.ID, FreshnessToken: open.FreshnessToken,
		Role: open.Role, Session: session(t), Report: json.RawMessage(`{"facts":["x"]}`),
	}); err == nil {
		t.Fatal("an abandoned change's assignment was answered")
	}
	// The documents are left exactly where they are.
	proposal, _ := final.DocumentPath(artifact.KindProposal)
	if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(proposal))); err != nil {
		t.Fatalf("the abandoned change's proposal was removed: %v", err)
	}
}

// mustJSON marshals a value or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// writeDocument replaces one document's body on disk, the way a host
// would.
func (h *harness) writeDocument(t *testing.T, st State, kind artifact.Kind, body string) {
	t.Helper()
	path, err := st.DocumentPath(kind)
	if err != nil {
		t.Fatalf("DocumentPath(%s): %v", kind, err)
	}
	abs := filepath.Join(h.root, filepath.FromSlash(path))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionWholeDocument {
			doc.Regions[i].Content = []byte(body)
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render %s: %v", path, err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readDocument returns one document's body, or "" when it does not exist.
func (h *harness) readDocument(t *testing.T, st State, kind artifact.Kind) string {
	t.Helper()
	path, err := st.DocumentPath(kind)
	if err != nil {
		t.Fatalf("DocumentPath(%s): %v", kind, err)
	}
	raw, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return string(doc.Region(artifact.RegionWholeDocument))
}
