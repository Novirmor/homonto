package change

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
	"github.com/noviopenworks/homonto/internal/task"
	"github.com/noviopenworks/homonto/internal/verify"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

var testNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

// scriptedEnv answers the engine's workspace questions from fields the
// test sets, so preflight can be exercised without a repository on disk.
type scriptedEnv struct {
	control   Member
	members   []Member
	base      task.Baseline
	sources   []fingerprint.Digest
	diff      []pathclass.DiffEntry
	classes   *workspacecfg.PathClasses
	checks    []verify.Set
	partition func(items []artifact.Item) []Unit
	// tripwireCandidate is unused by the environment itself; the ADR test
	// sets it to assert the gate names the candidate it settles.
	tripwireCandidate string
}

// Keep the Change tests on the shared workspace contract.
var _ task.Environment = (*scriptedEnv)(nil)

func (s *scriptedEnv) Control(context.Context) (Member, error)   { return s.control, nil }
func (s *scriptedEnv) Members(context.Context) ([]Member, error) { return s.members, nil }
func (s *scriptedEnv) Fingerprints(context.Context) (task.Baseline, error) {
	return s.base, nil
}

func (s *scriptedEnv) Partition(_ context.Context, _ identity.WorkID, items []artifact.Item) ([]Unit, error) {
	if s.partition == nil {
		return nil, nil
	}
	return s.partition(items), nil
}

func (s *scriptedEnv) Isolate(_ context.Context, _ identity.WorkID, actionID identity.ActionID, unit Unit) (Unit, error) {
	unit.Root = "work/" + string(actionID)
	return unit, nil
}

func (s *scriptedEnv) Integrations(_ context.Context, _ identity.WorkID, results []Result) ([]Unit, error) {
	if len(results) == 0 {
		return nil, nil
	}
	return []Unit{{
		Label: "integration", Member: s.control, Integration: true,
		Root: "work/integration", Scope: []string{"src"},
		Prompt: "Combine the parallel output.",
	}}, nil
}

func (s *scriptedEnv) SourceFingerprints(context.Context, identity.WorkID) ([]fingerprint.Digest, error) {
	return s.sources, nil
}

func (s *scriptedEnv) RunChecks(context.Context, identity.WorkID) (verify.Set, error) {
	if len(s.checks) == 0 {
		return verify.Set{}, nil
	}
	return s.checks[0], nil
}

func (s *scriptedEnv) ResultDiff(_ context.Context, action protocol.Action, _ Unit) (guard.ResultDiff, error) {
	return guard.ResultDiff{Root: action.WorkingDirectory}, nil
}

func (s *scriptedEnv) WorkspaceDiff(context.Context, identity.WorkID, []fingerprint.Digest) ([]pathclass.DiffEntry, error) {
	return s.diff, nil
}

func (s *scriptedEnv) Matchers(string) (*pathclass.Matcher, error) {
	return pathclass.NewMatcher(s.classes)
}

// harness is a whole Change engine over a real database and control root.
type harness struct {
	engine      *Engine
	env         *scriptedEnv
	assignments *assignment.Store
	artifacts   *artifact.Service
	root        string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessAt(t, t.TempDir())
}

// newHarnessAt builds an engine over a given control root with a FRESH
// database. Pointing it at an existing harness's root is how a second
// machine is modelled: the documents are there, the runtime is not.
func newHarnessAt(t *testing.T, root string) *harness {
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
	clock := func() time.Time { return testNow }

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
	controlID := mustRepoID(t)
	control := Member{ID: controlID, Path: ".", Git: true}
	env := &scriptedEnv{
		control: control,
		members: []Member{control},
		base: task.Baseline{
			Membership:  fingerprint.Bytes("members", []byte("v1")),
			PathClass:   fingerprint.Bytes("paths", []byte("v1")),
			CheckConfig: fingerprint.Bytes("checks", []byte("v1")),
		},
		sources: []fingerprint.Digest{fingerprint.Bytes("src", []byte("v1"))},
	}
	engine, err := NewEngine(Dependencies{
		DB: db, Assignments: assignments, Artifacts: artifacts, Findings: findings,
		Evidence: evidence, Archive: arch, Guard: g, Environment: env, Now: clock,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return &harness{engine: engine, env: env, assignments: assignments, artifacts: artifacts, root: root}
}

func mustRepoID(t *testing.T) identity.RepositoryID {
	t.Helper()
	id, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	return id
}

func session(t *testing.T) protocol.Session {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return protocol.Session{
		HostID: identity.SessionID(id), Hostname: "test-host", PID: 99,
		Executable: "/usr/bin/claude", StartedAt: testNow,
	}
}

// answer submits a report for one preflight assignment, reporting the
// given preset scope signals as finding ids.
func (h *harness) answer(t *testing.T, act protocol.Action, signals ...pathclass.Signal) {
	t.Helper()
	findings := make([]protocol.Finding, 0, len(signals))
	for _, s := range signals {
		findings = append(findings, protocol.Finding{
			ID: string(s), Severity: protocol.SeverityHigh,
			Summary:        "the request implies " + string(s),
			Evidence:       []string{"internal/x/y.go:42"},
			Recommendation: "consider the full path",
		})
	}
	var payload any
	switch act.Role {
	case protocol.RoleExplorer:
		payload = protocol.ExplorerReport{Facts: []string{"the login path is in src"}}
	case protocol.RoleSkeptic:
		payload = protocol.SkepticReport{
			Assumptions: []string{"the request is small"}, Findings: findings,
		}
	default:
		t.Fatalf("no canned report for role %q", act.Role)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if _, err := h.assignments.Submit(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        act.ID, FreshnessToken: act.FreshnessToken,
		Role: act.Role, Session: session(t), Report: raw,
	}); err != nil {
		t.Fatalf("Submit(%s): %v", act.Role, err)
	}
}

// assess walks a candidate through its assessment round and returns the
// classification gate.
func (h *harness) assess(t *testing.T, st PreflightState, signals ...pathclass.Signal) protocol.Action {
	t.Helper()
	for i := 0; i < 6; i++ {
		resp, err := h.engine.NextPreflight(t.Context(), st.WorkID)
		if err != nil {
			t.Fatalf("NextPreflight: %v", err)
		}
		if err := resp.Validate(); err != nil {
			t.Fatalf("invalid response: %v", err)
		}
		if resp.State == protocol.NextComplete {
			t.Fatal("the candidate finished without a classification gate")
		}
		if resp.State == protocol.NextBlocked {
			return resp.Actions[0]
		}
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleSkeptic {
				h.answer(t, act, signals...)
				continue
			}
			h.answer(t, act)
		}
	}
	t.Fatal("the candidate never reached a classification gate")
	return protocol.Action{}
}

func TestStartPreflightCreatesNothingPortable(t *testing.T) {
	h := newHarness(t)
	st, resp, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	if st.Step != PreflightAssess || st.Generation != 1 {
		t.Fatalf("state = %+v, want preflight_assess at generation 1", st)
	}
	if resp.State != protocol.NextReady || len(resp.Actions) == 0 {
		t.Fatalf("response = %+v, want assessment assignments", resp)
	}
	// The whole point of preflight: nothing on disk yet.
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(artifact.ChangesDir)))
	if err != nil {
		t.Fatalf("read changes dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != artifact.ArchiveName {
			t.Fatalf("preflight created %q before the human confirmed anything", e.Name())
		}
	}
	if _, err := h.engine.State(t.Context(), st.WorkID); !errors.Is(err, ErrUnknownChange) {
		t.Fatalf("a change exists before confirmation: %v", err)
	}
}

func TestStartPreflightValidatesItsInput(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "Fix Login", Request: "x",
	}); err == nil {
		t.Error("an invalid work name was accepted")
	}
	if _, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "   ",
	}); err == nil {
		t.Error("a blank request was accepted")
	}
}

// TestPreflightDispatchesBothReadOnlyRoles proves the assessment is not a
// guess: an explorer per member and a skeptic look at it first.
func TestPreflightDispatchesBothReadOnlyRoles(t *testing.T) {
	h := newHarness(t)
	extra := Member{ID: mustRepoID(t), Path: "services/api", Git: true}
	h.env.members = append(h.env.members, extra)

	st, resp, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	roles := map[protocol.Role]int{}
	for _, act := range resp.Actions {
		roles[act.Role]++
		if !act.WriteScope.ReadOnly {
			t.Fatalf("preflight assignment %s is writable; preflight observes only", act.ID)
		}
	}
	if roles[protocol.RoleExplorer] != 2 {
		t.Fatalf("explorers = %d, want one per member", roles[protocol.RoleExplorer])
	}
	if roles[protocol.RoleSkeptic] != 1 {
		t.Fatalf("skeptics = %d, want one", roles[protocol.RoleSkeptic])
	}
	_ = st
}

// TestSuggestionFollowsTheSignals proves the suggestion is derived from
// what was observed, and that its evidence is legible.
func TestSuggestionFollowsTheSignals(t *testing.T) {
	tests := []struct {
		name    string
		signals []pathclass.Signal
		want    Path
	}{
		{"no signals", nil, PathTweak},
		{"a public API change", []pathclass.Signal{pathclass.SignalPublicAPI}, PathFull},
		{"architecture", []pathclass.Signal{pathclass.SignalArchitecture}, PathFull},
		{"should split", []pathclass.Signal{pathclass.SignalShouldSplit}, PathFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
				Name: "fix-login", Request: "Login fails after a restart.",
			})
			if err != nil {
				t.Fatalf("StartPreflight: %v", err)
			}
			gate := h.assess(t, st, tt.signals...)
			after, err := h.engine.Preflight(t.Context(), st.WorkID)
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			if after.Suggestion.Path != tt.want {
				t.Fatalf("suggested %s, want %s (signals %v)",
					after.Suggestion.Path, tt.want, after.Suggestion.Signals)
			}
			if gate.Decision == nil ||
				gate.Decision.Kind != protocol.DecisionKind(decision.KindConfirmClassification) {
				t.Fatalf("gate = %+v, want a classification confirmation", gate.Decision)
			}
			if len(gate.Decision.Choices) != 3 {
				t.Fatalf("the gate offers %d choices, want all three paths", len(gate.Decision.Choices))
			}
			// Agreeing with the suggestion needs no rationale; every other
			// choice does.
			for _, c := range gate.Decision.Choices {
				wantRationale := Path(c.Value) != tt.want
				if c.RequiresRationale != wantRationale {
					t.Errorf("choice %q requires rationale = %v, want %v",
						c.Value, c.RequiresRationale, wantRationale)
				}
			}
			if len(tt.signals) > 0 && len(after.Suggestion.Evidence) == 0 {
				t.Fatal("a suggestion of Full came with no evidence")
			}
			if !strings.Contains(gate.Decision.Prompt, "Login fails after a restart.") {
				t.Fatalf("the gate does not show the request:\n%s", gate.Decision.Prompt)
			}
		})
	}
}

func TestConfirmPreflightCreatesTheChange(t *testing.T) {
	h := newHarness(t)
	st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	h.assess(t, st)

	change, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathTweak,
	})
	if err != nil {
		t.Fatalf("ConfirmPreflight: %v", err)
	}
	if change.Path != PathTweak || change.Step != firstStep(PathTweak) {
		t.Fatalf("change = %+v", change)
	}
	// The preset's documents now exist, seeded with the confirmed request.
	for _, kind := range []artifact.Kind{artifact.KindTweak, artifact.KindTasks} {
		path, err := change.DocumentPath(kind)
		if err != nil {
			t.Fatalf("DocumentPath(%s): %v", kind, err)
		}
		if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("%s was not created: %v", path, err)
		}
	}
	tweakPath, _ := change.DocumentPath(artifact.KindTweak)
	doc, err := h.artifacts.Read(t.Context(), artifact.Ref{
		WorkID: change.WorkID, Kind: artifact.KindTweak, Path: tweakPath,
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(doc.Region(artifact.RegionWholeDocument)), "Login fails after a restart.") {
		t.Fatalf("the confirmed request was not seeded: %q", doc.Region(artifact.RegionWholeDocument))
	}
	// The immutable work baseline was captured at this transition.
	if len(change.Baseline.Work) == 0 {
		t.Fatal("no work baseline was captured at the path-confirmed transition")
	}
	// The candidate is finished and cannot be confirmed twice.
	pre, err := h.engine.Preflight(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if pre.Step != PreflightConfirmed {
		t.Fatalf("candidate step = %s, want preflight_confirmed", pre.Step)
	}
	if _, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathTweak,
	}); !errors.Is(err, ErrPreflightFinished) {
		t.Fatalf("second ConfirmPreflight error = %v, want ErrPreflightFinished", err)
	}
}

// TestFullConfirmationCreatesOnlyTheProposal proves a Full change does not
// get a tasks.md in Open: that is a Design output.
func TestFullConfirmationCreatesOnlyTheProposal(t *testing.T) {
	h := newHarness(t)
	st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "rework-catalog", Request: "Replace the catalog storage layer.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	h.assess(t, st, pathclass.SignalArchitecture)
	change, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathFull,
	})
	if err != nil {
		t.Fatalf("ConfirmPreflight: %v", err)
	}
	proposal, _ := change.DocumentPath(artifact.KindProposal)
	if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(proposal))); err != nil {
		t.Fatalf("proposal.md was not created: %v", err)
	}
	tasks, _ := change.DocumentPath(artifact.KindTasks)
	if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(tasks))); err == nil {
		t.Fatal("a Full change was given a tasks.md in Open; that is a Design output")
	}
}

// TestOverridingTheSuggestionNeedsARationale proves the suggestion is
// evidence rather than a verdict, and that disagreeing is recorded.
func TestOverridingTheSuggestionNeedsARationale(t *testing.T) {
	h := newHarness(t)
	st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	h.assess(t, st) // no signals: Tweak is suggested

	if _, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathFull,
	}); !errors.Is(err, ErrOverrideNeedsRationale) {
		t.Fatalf("ConfirmPreflight error = %v, want ErrOverrideNeedsRationale", err)
	}
	// The refusal created nothing.
	if _, err := h.engine.State(t.Context(), st.WorkID); !errors.Is(err, ErrUnknownChange) {
		t.Fatalf("a refused confirmation created a change: %v", err)
	}
	change, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathFull,
		Rationale: "the storage layer has to change with it",
	})
	if err != nil {
		t.Fatalf("ConfirmPreflight with a rationale: %v", err)
	}
	if change.Path != PathFull {
		t.Fatalf("path = %s, want the human's choice", change.Path)
	}
}

func TestConfirmPreflightRefusesEarlyAndUnknown(t *testing.T) {
	h := newHarness(t)
	st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	// Still assessing: there is nothing to confirm yet.
	if _, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathTweak,
	}); !errors.Is(err, ErrNotConfirmable) {
		t.Fatalf("ConfirmPreflight error = %v, want ErrNotConfirmable", err)
	}
	unknown, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	if _, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: unknown, Path: PathTweak,
	}); !errors.Is(err, ErrUnknownPreflight) {
		t.Fatalf("ConfirmPreflight(unknown) error = %v, want ErrUnknownPreflight", err)
	}
	h.assess(t, st)
	if _, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: "sideways",
	}); err == nil {
		t.Fatal("an unknown path was confirmed")
	}
}

// TestAbandonedPreflightLeavesNothing proves a candidate is free to start.
func TestAbandonedPreflightLeavesNothing(t *testing.T) {
	h := newHarness(t)
	st, resp, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	open := resp.Actions[0]

	after, err := h.engine.AbandonPreflight(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("AbandonPreflight: %v", err)
	}
	if after.Step != PreflightAbandoned {
		t.Fatalf("step = %s, want preflight_abandoned", after.Step)
	}
	// The assignment a host was holding is dead.
	if _, err := h.assignments.Submit(t.Context(), protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        open.ID, FreshnessToken: open.FreshnessToken,
		Role: open.Role, Session: session(t), Report: json.RawMessage(`{"facts":["x"]}`),
	}); err == nil {
		t.Fatal("an abandoned candidate's assignment was answered")
	}
	// And the name is free again.
	if _, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Try again.",
	}); err != nil {
		t.Fatalf("the name was not released by abandoning: %v", err)
	}
	if _, err := h.engine.AbandonPreflight(t.Context(), st.WorkID); !errors.Is(err, ErrPreflightFinished) {
		t.Fatalf("abandoning twice error = %v, want ErrPreflightFinished", err)
	}
}

// TestActiveNameIsNotReusable proves two active changes cannot fight over
// the same documents.
func TestActiveNameIsNotReusable(t *testing.T) {
	h := newHarness(t)
	st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	h.assess(t, st)
	if _, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathTweak,
	}); err != nil {
		t.Fatalf("ConfirmPreflight: %v", err)
	}
	if _, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Something else.",
	}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("StartPreflight error = %v, want ErrNameTaken", err)
	}
}

// TestPresetScopeMeasuresFromTheImmutableBaseline proves the count uses
// the baseline captured at confirmation and the member's path classes.
func TestPresetScopeMeasuresFromTheImmutableBaseline(t *testing.T) {
	h := newHarness(t)
	h.env.classes = &workspacecfg.PathClasses{
		Tests:     []string{"**/*_test.go"},
		Generated: []string{"gen/**"},
	}
	st, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
		Name: "fix-login", Request: "Login fails after a restart.",
	})
	if err != nil {
		t.Fatalf("StartPreflight: %v", err)
	}
	h.assess(t, st)
	change, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
		WorkID: st.WorkID, Path: PathFix, Rationale: "it is a defect",
	})
	if err != nil {
		t.Fatalf("ConfirmPreflight: %v", err)
	}
	captured := append([]fingerprint.Digest(nil), change.Baseline.Work...)

	// Six source files, plus tests and generated output that do not count.
	h.env.diff = []pathclass.DiffEntry{
		{Member: ".", Path: "src/a.go", Op: pathclass.OpModified},
		{Member: ".", Path: "src/b.go", Op: pathclass.OpModified},
		{Member: ".", Path: "src/c.go", Op: pathclass.OpModified},
		{Member: ".", Path: "src/d.go", Op: pathclass.OpModified},
		{Member: ".", Path: "src/e.go", Op: pathclass.OpModified},
		{Member: ".", Path: "src/f.go", Op: pathclass.OpModified},
		{Member: ".", Path: "src/a_test.go", Op: pathclass.OpAdded},
		{Member: ".", Path: "gen/api.pb.go", Op: pathclass.OpModified},
	}
	assessment, err := h.engine.PresetScope(t.Context(), change, nil)
	if err != nil {
		t.Fatalf("PresetScope: %v", err)
	}
	if !assessment.Pause {
		t.Fatal("six counted files did not pause the preset")
	}
	if len(assessment.Signals) != 1 || assessment.Signals[0] != pathclass.SignalFileCount {
		t.Fatalf("Signals = %v, want only the file-count warning", assessment.Signals)
	}
	// The world moving does not move the baseline.
	h.env.sources = []fingerprint.Digest{fingerprint.Bytes("src", []byte("v2"))}
	again, err := h.engine.State(t.Context(), change.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(again.Baseline.Work) != len(captured) || again.Baseline.Work[0] != captured[0] {
		t.Fatalf("the work baseline moved: %v, want %v", again.Baseline.Work, captured)
	}
}

func TestNewEngineRequiresEveryCollaborator(t *testing.T) {
	if _, err := NewEngine(Dependencies{}); err == nil {
		t.Fatal("NewEngine with nothing = nil error, want rejection")
	}
	h := newHarness(t)
	if _, err := NewEngine(Dependencies{
		Assignments: h.assignments, Artifacts: h.artifacts, Environment: h.env,
	}); err == nil {
		t.Fatal("NewEngine without a database = nil error, want rejection")
	}
}
