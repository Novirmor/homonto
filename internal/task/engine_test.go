package task

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/finding"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/verify"
)

var engineNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

// scriptedEnv is a workspace the test drives directly: it answers the
// engine's questions about members, fingerprints, partitions, checks, and
// result diffs without needing a repository on disk. The engine's job is
// sequencing and gating, and that is what these tests exercise.
type scriptedEnv struct {
	control      Member
	members      []Member
	fingerprints Baseline
	sources      []fingerprint.Digest
	checks       []verify.Set
	checkIndex   int
	partitions   int
	diffs        map[string]guard.ResultDiff
}

func (s *scriptedEnv) Control(context.Context) (Member, error) { return s.control, nil }

func (s *scriptedEnv) Members(context.Context) ([]Member, error) { return s.members, nil }

func (s *scriptedEnv) Fingerprints(context.Context) (Baseline, error) { return s.fingerprints, nil }

func (s *scriptedEnv) Partition(_ context.Context, workID identity.WorkID, items []artifact.Item) ([]Partition, error) {
	if len(items) == 0 {
		return nil, nil
	}
	s.partitions++
	out := make([]Partition, 0, len(items))
	for _, it := range items {
		label := strings.ReplaceAll(it.Text, " ", "-")
		out = append(out, Partition{
			Label:  label,
			Member: s.control,
			Items:  []int{it.Index},
			Scope:  []string{"src"},
			Prompt: "Implement: " + it.Text,
		})
	}
	return out, nil
}

func (s *scriptedEnv) Isolate(_ context.Context, _ identity.WorkID, actionID identity.ActionID, unit Partition) (Partition, error) {
	unit.Root = "work/" + string(actionID)
	return unit, nil
}

func (s *scriptedEnv) Integrations(_ context.Context, _ identity.WorkID, results []Result) ([]Partition, error) {
	if len(results) == 0 {
		return nil, nil
	}
	for _, r := range results {
		if r.Material.Kind == "" {
			return nil, errors.New("an implementation result carried no material")
		}
	}
	return []Partition{{
		Label:       "integration",
		Member:      s.control,
		Integration: true,
		Root:        "work/integration",
		Scope:       []string{"src"},
		Prompt:      "Combine the parallel output into one branch.",
	}}, nil
}

func (s *scriptedEnv) SourceFingerprints(context.Context, identity.WorkID) ([]fingerprint.Digest, error) {
	return s.sources, nil
}

func (s *scriptedEnv) RunChecks(context.Context, identity.WorkID) (verify.Set, error) {
	if s.checkIndex >= len(s.checks) {
		return s.checks[len(s.checks)-1], nil
	}
	set := s.checks[s.checkIndex]
	s.checkIndex++
	return set, nil
}

func (s *scriptedEnv) ResultDiff(_ context.Context, action protocol.Action, _ Partition) (guard.ResultDiff, error) {
	if d, ok := s.diffs[action.WorkingDirectory]; ok {
		return d, nil
	}
	return guard.ResultDiff{
		Root:    action.WorkingDirectory,
		Changes: []guard.Change{{Path: "src/login.go", Kind: guard.ChangeModified}},
	}, nil
}

func (s *scriptedEnv) WorkspaceDiff(context.Context, identity.WorkID, []fingerprint.Digest) ([]pathclass.DiffEntry, error) {
	return nil, nil
}

func (s *scriptedEnv) Matchers(string) (*pathclass.Matcher, error) {
	return pathclass.NewMatcher(nil)
}

// checkSet builds a verification set with a single check of the given
// outcome, over inputs the test controls.
func checkSet(t *testing.T, repo identity.RepositoryID, outcome verify.Outcome) verify.Set {
	t.Helper()
	spec := verify.Spec{Name: "unit", Command: []string{"/bin/true"}, Timeout: time.Minute}
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
		At:     engineNow,
		Results: []verify.Result{{
			Spec: spec, SpecPin: pin, Outcome: outcome, ExitCode: exit, StartedAt: engineNow,
		}},
	}
}

// harness is a whole engine over a real database and a real control root.
type harness struct {
	engine      *Engine
	env         *scriptedEnv
	assignments *assignment.Store
	artifacts   *artifact.Service
	findings    *finding.Service
	root        string
}

func newHarness(t *testing.T, checks ...verify.Set) *harness {
	t.Helper()
	return newHarnessAt(t, t.TempDir(), checks...)
}

// newHarnessAt builds an engine over a given control root with a FRESH
// database. Pointing it at an existing harness's root is how a second
// machine is modelled: the documents are there, the runtime is not.
func newHarnessAt(t *testing.T, root string, checks ...verify.Set) *harness {
	t.Helper()
	for _, sub := range archive.Dirs() {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	db, err := store.Open(context.Background(),
		filepath.Join(t.TempDir(), "homonto.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := func() time.Time { return engineNow }

	assignments, err := assignment.NewStore(context.Background(), db, clock)
	if err != nil {
		t.Fatalf("assignment.NewStore: %v", err)
	}
	journal, err := artifact.NewStoreJournal(db)
	if err != nil {
		t.Fatalf("artifact.NewStoreJournal: %v", err)
	}
	artifacts, err := artifact.NewService(root, journal, clock)
	if err != nil {
		t.Fatalf("artifact.NewService: %v", err)
	}
	findings, err := finding.NewService(db, clock)
	if err != nil {
		t.Fatalf("finding.NewService: %v", err)
	}
	evidence, err := verify.NewStore(db, clock)
	if err != nil {
		t.Fatalf("verify.NewStore: %v", err)
	}
	arch, err := archive.NewService(root)
	if err != nil {
		t.Fatalf("archive.NewService: %v", err)
	}
	g, err := guard.New(assignments, journal)
	if err != nil {
		t.Fatalf("guard.New: %v", err)
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	control := Member{ID: repoID, Path: ".", Git: true}
	if len(checks) == 0 {
		checks = []verify.Set{checkSet(t, repoID, verify.OutcomePassed)}
	}
	env := &scriptedEnv{
		control: control,
		members: []Member{control},
		fingerprints: Baseline{
			Membership:  fingerprint.Bytes("members", []byte("v1")),
			PathClass:   fingerprint.Bytes("paths", []byte("v1")),
			CheckConfig: fingerprint.Bytes("checks", []byte("v1")),
		},
		sources: []fingerprint.Digest{fingerprint.Bytes("src", []byte("v1"))},
		checks:  checks,
		diffs:   map[string]guard.ResultDiff{},
	}
	engine, err := NewEngine(Dependencies{
		DB: db, Assignments: assignments, Artifacts: artifacts, Findings: findings,
		Evidence: evidence, Archive: arch, Guard: g, Environment: env, Now: clock,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return &harness{
		engine: engine, env: env, assignments: assignments,
		artifacts: artifacts, findings: findings, root: root,
	}
}

// next asks the engine what to do and fails if the response is invalid.
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

func session(t *testing.T) protocol.Session {
	t.Helper()
	id, err := identity.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return protocol.Session{
		HostID: id, Hostname: "test-host", PID: 1234,
		Executable: "/usr/bin/claude", StartedAt: engineNow,
	}
}

// answer submits a role-appropriate report for one action.
func (h *harness) answer(t *testing.T, act protocol.Action, findings []protocol.Finding, questions []protocol.Question) {
	t.Helper()
	var payload any
	switch act.Role {
	case protocol.RoleExplorer:
		payload = protocol.ExplorerReport{Facts: []string{"the login path is in src"}, Questions: questions}
	case protocol.RoleSkeptic:
		payload = protocol.SkepticReport{
			Assumptions: []string{"the store is the only writer"},
			Findings:    findings, Questions: questions,
		}
	case protocol.RoleReviewer:
		payload = protocol.ReviewerReport{
			Acceptance: []string{"the checklist is covered"},
			Findings:   findings, Questions: questions,
		}
	case protocol.RoleImplementer:
		payload = protocol.ImplementerReport{
			Material: protocol.Material{
				Kind: protocol.MaterialSnapshotPatch, PatchManifest: []string{"src/login.go"},
				Content: fingerprint.Bytes("material", []byte(act.ID)),
			},
			ChangedPaths: []string{"src/login.go"},
			Questions:    questions,
		}
	default:
		t.Fatalf("no canned report for role %q", act.Role)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if _, err := h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        act.ID,
		FreshnessToken:  act.FreshnessToken,
		Role:            act.Role,
		Session:         session(t),
		Report:          raw,
	}); err != nil {
		t.Fatalf("SubmitReport(%s %s): %v", act.Role, act.ID, err)
	}
}

// draft performs the host's edit under the action's grant.
func (h *harness) draft(t *testing.T, act protocol.Action, goal, checklist string) {
	t.Helper()
	if act.Kind != protocol.KindEdit || act.Edit == nil {
		t.Fatalf("action %s is not an edit action", act.ID)
	}
	abs := filepath.Join(h.root, filepath.FromSlash(act.Edit.Document))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse document: %v", err)
	}
	for i := range doc.Regions {
		switch doc.Regions[i].Region {
		case artifact.RegionTaskGoal:
			doc.Regions[i].Content = []byte(goal)
		case artifact.RegionTaskChecklist:
			doc.Regions[i].Content = []byte(checklist)
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render document: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := h.engine.AcceptEdit(t.Context(), act.ID, act.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
}

func TestStartCreatesTheDocumentAndTheFirstStep(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login", Goal: "Make login work."})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.Step != StepPlanExplore || st.Generation != 1 {
		t.Fatalf("state = %+v, want plan_explore at generation 1", st)
	}
	doc, err := h.artifacts.Read(t.Context(), st.ref())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(doc.Region(artifact.RegionTaskGoal)), "Make login work.") {
		t.Fatalf("goal region = %q", doc.Region(artifact.RegionTaskGoal))
	}
	if _, err := h.engine.Start(t.Context(), StartInput{Name: "Fix Login"}); err == nil {
		t.Fatal("Start with an invalid work name = nil error, want rejection")
	}
}

func TestNewEngineRequiresEveryCollaborator(t *testing.T) {
	h := newHarness(t)
	full := Dependencies{
		DB: nil, Assignments: h.assignments, Artifacts: h.artifacts,
		Findings: h.findings, Environment: h.env,
	}
	if _, err := NewEngine(full); err == nil {
		t.Fatal("NewEngine without a database = nil error, want rejection")
	}
	if _, err := NewEngine(Dependencies{}); err == nil {
		t.Fatal("NewEngine with nothing = nil error, want rejection")
	}
}

// drive runs a whole task from Start to a stopping point, answering every
// action the engine issues with the canned response for its role. It
// returns the final state and stops when the workflow is terminal or when
// stop says so.
func (h *harness) drive(t *testing.T, st State, stop func(protocol.NextResponse) bool) State {
	t.Helper()
	for i := 0; i < 60; i++ {
		resp := h.next(t, st.WorkID)
		if resp.State == protocol.NextComplete {
			break
		}
		if stop != nil && stop(resp) {
			break
		}
		for _, act := range resp.Actions {
			switch act.Kind {
			case protocol.KindEdit:
				h.draft(t, act, "Make login work.\n", "- [ ] fix the session store\n")
			case protocol.KindDecision:
				h.decide(t, act, "answered", "", "because")
			default:
				h.answer(t, act, nil, nil)
			}
		}
		var err error
		st, err = h.engine.State(t.Context(), st.WorkID)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		if st.Step.Terminal() {
			break
		}
	}
	final, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	return final
}

// decide answers a decision action.
func (h *harness) decide(t *testing.T, act protocol.Action, choice, answer, rationale string) {
	t.Helper()
	sub := decision.Submission{
		ActionID:       act.ID,
		FreshnessToken: act.FreshnessToken,
		Choice:         choice,
		Rationale:      rationale,
	}
	if act.Decision != nil && act.Decision.Kind == protocol.DecisionKind(decision.KindAnswerQuestion) {
		sub.Answer = "yes, do it the simple way"
		sub.Rationale = ""
	}
	if _, err := h.engine.Decide(t.Context(), sub); err != nil {
		t.Fatalf("Decide(%s): %v", act.ID, err)
	}
}

// TestHappyPathReachesTheArchive walks a whole task: four roles, a host
// draft, parallel implementation, integration, passing checks, a clean
// review, and one archived record.
func TestHappyPathReachesTheArchive(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login", Goal: "Make login work."})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The first thing offered is the explorer round, one per member.
	first := h.next(t, st.WorkID)
	if first.State != protocol.NextReady || len(first.Actions) != 1 ||
		first.Actions[0].Role != protocol.RoleExplorer {
		t.Fatalf("first actions = %+v, want one explorer", first.Actions)
	}

	final := h.drive(t, st, nil)
	if final.Step != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}

	// The document left the active tree for the archive, checked off, with
	// its evidence appended.
	if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(artifact.TasksDir), "fix-login.md")); err == nil {
		t.Fatal("the task document is still in the active tree")
	}
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(archive.TasksArchiveDir)))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive holds %d entries, want 1", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(archive.TasksArchiveDir), entries[0].Name()))
	if err != nil {
		t.Fatalf("read archived document: %v", err)
	}
	body := string(raw)
	for _, want := range []string{"- [x] fix the session store", "## Verification", "## Accepted deviations"} {
		if !strings.Contains(body, want) {
			t.Errorf("the archived record does not carry %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "- [ ]") {
		t.Errorf("the archived record still has an unchecked item:\n%s", body)
	}

	// Nothing further is offered, and the terminal state refuses new work.
	if resp := h.next(t, st.WorkID); resp.State != protocol.NextComplete {
		t.Fatalf("Next after archive = %+v, want complete", resp)
	}
}

// TestEveryRoleIsUsed proves all four roles are mandatory: the walk issues
// explorer, implementer, reviewer, and skeptic assignments.
func TestEveryRoleIsUsed(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	seen := map[protocol.Role]bool{}
	for i := 0; i < 60; i++ {
		resp := h.next(t, st.WorkID)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			if act.Role != "" {
				seen[act.Role] = true
			}
			switch act.Kind {
			case protocol.KindEdit:
				h.draft(t, act, "Make login work.\n", "- [ ] fix the session store\n")
			case protocol.KindDecision:
				h.decide(t, act, "answered", "", "because")
			default:
				h.answer(t, act, nil, nil)
			}
		}
		if cur, err := h.engine.State(t.Context(), st.WorkID); err == nil && cur.Step.Terminal() {
			break
		}
	}
	for _, role := range []protocol.Role{
		protocol.RoleExplorer, protocol.RoleImplementer,
		protocol.RoleReviewer, protocol.RoleSkeptic,
	} {
		if !seen[role] {
			t.Errorf("the %s role was never assigned", role)
		}
	}
}

// TestBlockingQuestionsGateImplementation proves a question raised in Plan
// is put to the human and blocks until answered — a subagent cannot
// satisfy a human decision gate itself.
func TestBlockingQuestionsGateImplementation(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Explorer round, with a question attached.
	resp := h.next(t, st.WorkID)
	h.answer(t, resp.Actions[0], nil, []protocol.Question{{
		ID: "Q-1", Text: "Should the session store be replaced?",
		Consequence: "It changes the migration path.",
	}})
	// Draft, then skeptic.
	resp = h.next(t, st.WorkID)
	h.draft(t, resp.Actions[0], "Make login work.\n", "- [ ] fix the session store\n")
	resp = h.next(t, st.WorkID)
	h.answer(t, resp.Actions[0], nil, nil)

	// The question is now a blocking decision, offered alone.
	resp = h.next(t, st.WorkID)
	if resp.State != protocol.NextBlocked || len(resp.Actions) != 1 {
		t.Fatalf("response = %+v, want exactly one blocking decision", resp)
	}
	gate := resp.Actions[0]
	if gate.Decision == nil || gate.Decision.QuestionID != "Q-1" {
		t.Fatalf("decision = %+v, want the question gate", gate.Decision)
	}
	// Asking again returns the same gate: nothing moves until it is
	// answered.
	if again := h.next(t, st.WorkID); again.Actions[0].ID != gate.ID {
		t.Fatal("the blocking gate was reissued instead of held")
	}
	cur, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if cur.Step != StepPlanResolve {
		t.Fatalf("step = %s, want plan_resolve while the question is open", cur.Step)
	}

	h.decide(t, gate, "answered", "yes", "")
	resp = h.next(t, st.WorkID)
	if resp.Actions[0].Role != protocol.RoleImplementer {
		t.Fatalf("after the answer the next action is %+v, want an implementer", resp.Actions[0])
	}
}

// TestChecksRunBeforeReview proves Homonto runs the checks itself and that
// a failure sends the workflow to repair rather than to review.
func TestChecksRunBeforeReview(t *testing.T) {
	h := newHarness(t)
	repoID := h.env.control.ID
	h.env.checks = []verify.Set{
		checkSet(t, repoID, verify.OutcomeFailed),
		checkSet(t, repoID, verify.OutcomePassed),
	}
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	reachedRepair := false
	final := h.drive(t, st, func(resp protocol.NextResponse) bool {
		cur, err := h.engine.State(t.Context(), st.WorkID)
		if err == nil && cur.Step == StepDoRepair {
			reachedRepair = true
		}
		return false
	})
	if !reachedRepair {
		t.Fatal("a failing check did not send the workflow to repair")
	}
	if final.Step != StepArchived {
		t.Fatalf("final step = %s, want archived after the repair succeeded", final.Step)
	}
}

// TestBlockingFindingsGateCompletion proves a critical finding blocks and
// that clearing it lets the task finish.
func TestBlockingFindingsGateCompletion(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	blocking := []protocol.Finding{{
		ID: "F-1", Severity: protocol.SeverityCritical,
		Summary: "the fix drops sessions on restart", Evidence: []string{"src/login.go:42"},
		Recommendation: "persist the session before restarting",
	}}
	// Walk to the review round, then let the reviewer raise a blocker.
	st = h.drive(t, st, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleReviewer {
				return true
			}
		}
		return false
	})
	if st.Step != StepDoneReview {
		t.Fatalf("step = %s, want done_review", st.Step)
	}
	resp := h.next(t, st.WorkID)
	for _, act := range resp.Actions {
		if act.Role == protocol.RoleReviewer {
			h.answer(t, act, blocking, nil)
			continue
		}
		h.answer(t, act, nil, nil)
	}
	blockers, err := h.findings.Blockers(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ExternalID != "F-1" {
		t.Fatalf("blockers = %+v, want the critical finding", blockers)
	}
	// The review is answered, but the blocker sends the workflow to repair
	// rather than to completion.
	if _, err := h.engine.Next(t.Context(), st.WorkID); err != nil {
		t.Fatalf("Next: %v", err)
	}
	after, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if after.Step == StepArchived || after.Step == StepDoneFinalize {
		t.Fatalf("step = %s; a critical finding must block completion", after.Step)
	}
	if after.Step != StepDoRepair {
		t.Fatalf("step = %s, want do_repair", after.Step)
	}
}

// TestThirdRepairFailureAsksTheHuman is the bounded repair loop: after
// three failed rounds Homonto stops and puts the choice to a person.
func TestThirdRepairFailureAsksTheHuman(t *testing.T) {
	h := newHarness(t)
	repoID := h.env.control.ID
	failing := checkSet(t, repoID, verify.OutcomeFailed)
	h.env.checks = []verify.Set{failing, failing, failing, failing, failing, failing}
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var gate protocol.Action
	h.drive(t, st, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Decision != nil &&
				act.Decision.Kind == protocol.DecisionKind(decision.KindRepairLimit) {
				gate = act
				return true
			}
		}
		return false
	})
	if gate.ID == "" {
		t.Fatal("three failed repair rounds did not reach the human")
	}
	if gate.Decision.FindingID != "" {
		t.Fatalf("the repair-limit gate carries a finding id: %+v", gate.Decision)
	}
	values := map[string]bool{}
	for _, c := range gate.Decision.Choices {
		values[c.Value] = true
		if !c.RequiresRationale {
			t.Errorf("choice %q needs no rationale; after three failures every choice needs one", c.Value)
		}
	}
	for _, want := range []string{"continue", "accept", "abandon"} {
		if !values[want] {
			t.Errorf("the repair-limit gate offers no %q choice: %+v", want, gate.Decision.Choices)
		}
	}
	rounds, err := h.findings.RepairRounds(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("RepairRounds: %v", err)
	}
	if rounds < finding.RepairLimit {
		t.Fatalf("repair rounds = %d, want at least %d", rounds, finding.RepairLimit)
	}

	// Abandoning is one of the ways out and it stops the workflow.
	h.decide(t, gate, "abandon", "", "three rounds failed; we are changing approach")
	final, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if _, err := h.engine.Next(t.Context(), st.WorkID); err != nil {
		t.Fatalf("Next after the abandon decision: %v", err)
	}
	final, err = h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if final.Step != StepAbandoned {
		t.Fatalf("final step = %s, want abandoned", final.Step)
	}
}

func TestAbandonStopsTheWorkflowAndClosesOpenActions(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp := h.next(t, st.WorkID)
	open := resp.Actions[0]

	final, err := h.engine.Abandon(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if final.Step != StepAbandoned {
		t.Fatalf("step = %s, want abandoned", final.Step)
	}
	// The action a host was holding can no longer be answered.
	if _, err := h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        open.ID, FreshnessToken: open.FreshnessToken,
		Role: open.Role, Session: session(t), Report: json.RawMessage(`{"facts":["x"]}`),
	}); !errors.Is(err, ErrTerminal) {
		t.Fatalf("SubmitReport after abandon error = %v, want ErrTerminal", err)
	}
	if resp := h.next(t, st.WorkID); resp.State != protocol.NextComplete {
		t.Fatalf("Next after abandon = %+v, want complete", resp)
	}
	if _, err := h.engine.Abandon(t.Context(), st.WorkID); err == nil {
		t.Fatal("abandoning twice = nil error, want refusal")
	}
}

// TestOutOfScopeResultIsRefused proves the final-diff gate: a report backed
// by changes the assignment was not issued to make is refused rather than
// recorded, so nothing downstream ever reads it.
func TestOutOfScopeResultIsRefused(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp := h.next(t, st.WorkID)
	h.answer(t, resp.Actions[0], nil, nil)
	resp = h.next(t, st.WorkID)
	h.draft(t, resp.Actions[0], "Make login work.\n", "- [ ] fix the session store\n")
	resp = h.next(t, st.WorkID)
	h.answer(t, resp.Actions[0], nil, nil)
	resp = h.next(t, st.WorkID)
	impl := resp.Actions[0]
	if impl.Role != protocol.RoleImplementer {
		t.Fatalf("expected an implementer, got %+v", impl)
	}
	// The implementer also touched something far outside its scope.
	h.env.diffs[impl.WorkingDirectory] = guard.ResultDiff{
		Root: impl.WorkingDirectory,
		Changes: []guard.Change{
			{Path: "src/login.go", Kind: guard.ChangeModified},
			{Path: "internal/secret/keys.go", Kind: guard.ChangeModified},
		},
	}
	raw, err := json.Marshal(protocol.ImplementerReport{
		Material: protocol.Material{
			Kind: protocol.MaterialSnapshotPatch, PatchManifest: []string{"src/login.go"},
			Content: fingerprint.Bytes("material", []byte("x")),
		},
		ChangedPaths: []string{"src/login.go"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        impl.ID, FreshnessToken: impl.FreshnessToken,
		Role: impl.Role, Session: session(t), Report: raw,
	})
	if !errors.Is(err, ErrResultRejected) {
		t.Fatalf("SubmitReport error = %v, want ErrResultRejected", err)
	}
	// The refusal left the action answerable, so a corrected result can
	// still be submitted.
	delete(h.env.diffs, impl.WorkingDirectory)
	if _, err := h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        impl.ID, FreshnessToken: impl.FreshnessToken,
		Role: impl.Role, Session: session(t), Report: raw,
	}); err != nil {
		t.Fatalf("SubmitReport after correcting the diff: %v", err)
	}
}

// TestReconcileReturnsToTheEarliestAffectedStep proves the recorded step is
// never trusted alone: editing the goal at review time sends the workflow
// back to the draft, invalidates the open actions, and bumps the
// generation so a late answer is refused.
func TestReconcileReturnsToTheEarliestAffectedStep(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var review protocol.Action
	h.drive(t, st, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleReviewer {
				review = act
				return true
			}
		}
		return false
	})
	if review.ID == "" {
		t.Fatal("the workflow never reached review")
	}
	before, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	// Someone edits the goal behind the workflow's back.
	abs := filepath.Join(h.root, filepath.FromSlash(artifact.TasksDir), "fix-login.md")
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionTaskGoal {
			doc.Regions[i].Content = []byte("Actually, rewrite the whole auth stack.\n")
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	after, invalidations, err := h.engine.Reconcile(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(invalidations) != 1 || invalidations[0].Cause != CauseDocument {
		t.Fatalf("invalidations = %+v, want one document cause", invalidations)
	}
	if after.Step != StepPlanDraft {
		t.Fatalf("step = %s, want plan_draft", after.Step)
	}
	if after.Generation <= before.Generation {
		t.Fatalf("generation %d did not advance past %d", after.Generation, before.Generation)
	}
	// The reviewer's action is dead: a late answer cannot be recorded.
	if _, err := h.engine.SubmitReport(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        review.ID, FreshnessToken: review.FreshnessToken,
		Role: review.Role, Session: session(t),
		Report: json.RawMessage(`{"acceptance":["fine"]}`),
	}); err == nil {
		t.Fatal("a late answer to an invalidated action was accepted")
	}
}

// TestMembershipChangeReturnsToExplore proves a repository joining the
// workspace makes the explorers survey it.
func TestMembershipChangeReturnsToExplore(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.drive(t, st, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleReviewer {
				return true
			}
		}
		return false
	})
	h.env.fingerprints.Membership = fingerprint.Bytes("members", []byte("v2"))
	after, invalidations, err := h.engine.Reconcile(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(invalidations) != 1 || invalidations[0].Cause != CauseMembership {
		t.Fatalf("invalidations = %+v, want one membership cause", invalidations)
	}
	if after.Step != StepPlanExplore {
		t.Fatalf("step = %s, want plan_explore", after.Step)
	}
	if len(after.Baseline.Sources) != 0 {
		t.Fatalf("a return to planning kept %d integrated source(s)", len(after.Baseline.Sources))
	}
}

// TestPathClassChangeReturnsToExplore proves a scope classification change
// re-runs the survey rather than reusing scopes derived from the old one.
func TestPathClassChangeReturnsToExplore(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.drive(t, st, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleImplementer {
				return true
			}
		}
		return false
	})
	h.env.fingerprints.PathClass = fingerprint.Bytes("paths", []byte("v2"))
	after, invalidations, err := h.engine.Reconcile(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(invalidations) != 1 || invalidations[0].Cause != CausePathClass {
		t.Fatalf("invalidations = %+v, want one path-class cause", invalidations)
	}
	if after.Step != StepPlanExplore {
		t.Fatalf("step = %s, want plan_explore", after.Step)
	}
}

// TestSourceDriftReturnsOnlyToChecks proves the spec's exception: the goal
// did not change just because the integrated code did.
func TestSourceDriftReturnsOnlyToChecks(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.drive(t, st, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleReviewer {
				return true
			}
		}
		return false
	})
	h.env.sources = []fingerprint.Digest{fingerprint.Bytes("src", []byte("v2"))}
	after, invalidations, err := h.engine.Reconcile(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(invalidations) != 1 || invalidations[0].Cause != CauseSource {
		t.Fatalf("invalidations = %+v, want one source cause", invalidations)
	}
	if after.Step != StepDoneChecks {
		t.Fatalf("step = %s, want done_checks", after.Step)
	}
}

func TestReconcileIsQuietWhenNothingMoved(t *testing.T) {
	h := newHarness(t)
	st, err := h.engine.Start(t.Context(), StartInput{Name: "fix-login"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	after, invalidations, err := h.engine.Reconcile(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(invalidations) != 0 {
		t.Fatalf("invalidations = %+v, want none", invalidations)
	}
	if after.Step != st.Step || after.Generation != st.Generation {
		t.Fatalf("Reconcile moved a task that did not drift: %+v", after)
	}
}

func TestUnknownWorkIsReported(t *testing.T) {
	h := newHarness(t)
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	if _, err := h.engine.State(t.Context(), id); !errors.Is(err, ErrUnknownWork) {
		t.Fatalf("State error = %v, want ErrUnknownWork", err)
	}
	if _, err := h.engine.Next(t.Context(), id); !errors.Is(err, ErrUnknownWork) {
		t.Fatalf("Next error = %v, want ErrUnknownWork", err)
	}
	if _, _, err := h.engine.Reconcile(t.Context(), id); !errors.Is(err, ErrUnknownWork) {
		t.Fatalf("Reconcile error = %v, want ErrUnknownWork", err)
	}
}

// TestResumeRestoresAnAttachedTaskAtThePhaseStart is what makes a handed-
// off Task workable on the machine that picked it up.
//
// Attach rebuilds the portable facts; the workflow's own state machine is
// not portable and does not travel. Without Resume the work exists in the
// runtime and no command can advance it — `homonto next` reports no active
// work on a machine that just attached one.
//
// It restores the FIRST step of the recorded phase rather than the exact
// step the other machine was on. The checkpoint is content-free by design:
// it says which phase was reached and nothing about what happened inside
// it, so re-entering the phase re-derives that. A later step would claim
// evidence that never arrived.
func TestResumeRestoresAnAttachedTaskAtThePhaseStart(t *testing.T) {
	for _, tc := range []struct {
		phase artifact.Phase
		want  Step
	}{
		{artifact.PhasePlan, StepPlanExplore},
		{artifact.PhaseDo, StepDoImplement},
		{artifact.PhaseDone, StepDoneChecks},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			h := newHarness(t)
			started, err := h.engine.Start(context.Background(), StartInput{Name: "resume-me"})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			// A fresh machine: the documents are there, the state is not.
			fresh := newHarnessAt(t, h.root)
			st, err := fresh.engine.Resume(context.Background(), started.WorkID, started.Name, tc.phase)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if st.Step != tc.want {
				t.Errorf("resumed at %s, want %s", st.Step, tc.want)
			}
			if st.WorkID != started.WorkID || st.Name != started.Name {
				t.Errorf("resumed a different work: %s %q", st.WorkID, st.Name)
			}
		})
	}
}

// TestResumeLeavesAnAlreadyKnownTaskAlone: resuming must be safe to run
// again. Attach is journaled and can replay, and a second Resume that
// rewound a live task to the start of its phase would throw away
// everything the machine has done since.
func TestResumeLeavesAnAlreadyKnownTaskAlone(t *testing.T) {
	h := newHarness(t)
	started, err := h.engine.Start(context.Background(), StartInput{Name: "already-here"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	advanced, err := h.engine.Next(context.Background(), started.WorkID)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	_ = advanced
	before, err := h.engine.State(context.Background(), started.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	after, err := h.engine.Resume(context.Background(), started.WorkID, started.Name, artifact.PhaseDone)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if after.Step != before.Step || after.Generation != before.Generation {
		t.Errorf("Resume moved a known task from %s/%d to %s/%d",
			before.Step, before.Generation, after.Step, after.Generation)
	}
}
