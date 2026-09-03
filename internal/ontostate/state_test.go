package ontostate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/bypasslog"
)

func TestParse_ValidYAML_DerivesBuildPhase(t *testing.T) {
	input := []byte(`
change: onto-binary-foundation
workflow: full
phase: build
created: "2026-07-10"
base_ref: main
deps:
  - other-change
`)

	state, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if state.Change != "onto-binary-foundation" {
		t.Errorf("Change = %q, want %q", state.Change, "onto-binary-foundation")
	}
	if state.Phase != "build" {
		t.Errorf("Phase = %q, want %q", state.Phase, "build")
	}

	phase, err := state.DerivePhase()
	if err != nil {
		t.Fatalf("DerivePhase returned unexpected error: %v", err)
	}
	if phase != "build" {
		t.Errorf("DerivePhase() = %q, want %q", phase, "build")
	}
}

func TestParse_MalformedYAML_ErrorMentionsOntoState(t *testing.T) {
	input := []byte("change: [unterminated\n  phase: build")

	_, err := Parse(input)
	if err == nil {
		t.Fatal("Parse returned nil error for malformed YAML, want error")
	}
	if !strings.Contains(err.Error(), "onto-state") {
		t.Errorf("Parse error = %q, want it to contain %q", err.Error(), "onto-state")
	}
}

func TestValidate_UnknownPhase_ReturnsError(t *testing.T) {
	state := State{Change: "onto-binary-foundation", Phase: "bogus"}

	err := state.Validate()
	if err == nil {
		t.Fatal("Validate returned nil error for unknown phase, want error")
	}
}

func TestValidate_EmptyChange_ReturnsError(t *testing.T) {
	state := State{Change: "", Phase: "build"}

	err := state.Validate()
	if err == nil {
		t.Fatal("Validate returned nil error for empty change, want error")
	}
}

func TestLoad_MissingFile_ErrorNamesPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "onto-state.yaml")

	_, err := Load(missing)
	if err == nil {
		t.Fatal("Load returned nil error for missing file, want error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("Load error = %q, want it to contain path %q", err.Error(), missing)
	}
}

func TestLoad_ValidFile_ReturnsState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onto-state.yaml")
	content := []byte("change: onto-binary-foundation\nphase: open\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	state, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if state.Change != "onto-binary-foundation" {
		t.Errorf("Change = %q, want %q", state.Change, "onto-binary-foundation")
	}
	if state.Phase != "open" {
		t.Errorf("Phase = %q, want %q", state.Phase, "open")
	}
}

func TestLoad_MalformedFile_ErrorNamesPathAndProblem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onto-state.yaml")
	content := []byte("change: [unterminated\n  phase: build")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load returned nil error for malformed file, want error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load error = %q, want it to contain path %q", err.Error(), path)
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("Load error = %q, want it to indicate the YAML/parse problem", err.Error())
	}
}

func TestParse_GarbageBytes_DoesNotPanicAndReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Parse panicked on garbage bytes: %v", r)
		}
	}()

	_, err := Parse([]byte("\x00\x01garbage"))
	if err == nil {
		t.Fatal("Parse returned nil error for garbage bytes, want error")
	}
}

func TestDerivePhase_InvalidState_ReturnsValidateError(t *testing.T) {
	state := State{Change: "", Phase: "build"}

	_, err := state.DerivePhase()
	if err == nil {
		t.Fatal("DerivePhase returned nil error for invalid state, want error")
	}
}

func TestMarshalParse_RoundTrip_SimpleState(t *testing.T) {
	state := State{Change: "c", Phase: "build"}

	b, err := Marshal(state)
	if err != nil {
		t.Fatalf("Marshal returned unexpected error: %v", err)
	}

	got, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Errorf("Parse(Marshal(state)) = %+v, want %+v", got, state)
	}
}

func TestMarshalParse_RoundTrip_FullState(t *testing.T) {
	state := State{
		Change:   "onto-binary-foundation",
		Workflow: "full",
		Phase:    "verify",
		Created:  "2026-07-10",
		BaseRef:  "main",
		Deps:     []string{"other-change", "another-change"},
		Archived: true,
	}

	b, err := Marshal(state)
	if err != nil {
		t.Fatalf("Marshal returned unexpected error: %v", err)
	}

	got, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Errorf("Parse(Marshal(state)) = %+v, want %+v", got, state)
	}
}

func TestSave_NonExistentSubdir_CreatesDirAndFileAndCleansUpTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "onto-state.yaml")
	state := State{Change: "onto-binary-foundation", Phase: "open"}

	if err := Save(path, state); err != nil {
		t.Fatalf("Save returned unexpected error: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	want := state
	want.SchemaVersion = CurrentSchemaVersion // Save now stamps the current schema version
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load(Save(state)) = %+v, want %+v", got, want)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected temp file %s to be gone, stat err = %v", path+".tmp", err)
	}
}

func TestRequiredArtifacts_OpenPhase_WorkflowAware(t *testing.T) {
	// full: the task list is derived from the confirmed design, so tasks.md does
	// NOT gate the open exit.
	if got, want := RequiredArtifacts("open", "full"), []string{"onto-state.yaml", "proposal.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredArtifacts(open, full) = %v, want %v", got, want)
	}
	// empty/unknown workflow is treated as full (strictest).
	if got, want := RequiredArtifacts("open", ""), []string{"onto-state.yaml", "proposal.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredArtifacts(open, \"\") = %v, want %v", got, want)
	}
	// presets: tasks.md is the open-lite checklist, required from the open exit.
	if got, want := RequiredArtifacts("open", "fix"), []string{"onto-state.yaml", "proposal.md", "tasks.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredArtifacts(open, fix) = %v, want %v", got, want)
	}
}

func TestValidateSkeleton_AllArtifactsPresent_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	state := State{Change: "onto-binary-foundation", Phase: "open"}
	if err := Save(filepath.Join(dir, "onto-state.yaml"), state); err != nil {
		t.Fatalf("Save returned unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proposal.md"), []byte("proposal"), 0o644); err != nil {
		t.Fatalf("failed to write proposal.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte("tasks"), 0o644); err != nil {
		t.Fatalf("failed to write tasks.md: %v", err)
	}

	if err := ValidateSkeleton(dir); err != nil {
		t.Errorf("ValidateSkeleton returned unexpected error: %v", err)
	}
}

func TestValidateSkeleton_MissingTasksFile_ErrorNamesFile(t *testing.T) {
	dir := t.TempDir()
	// A fix preset requires tasks.md from the open exit (its open-lite checklist),
	// so an open preset missing tasks.md is a skeleton error naming the file.
	state := State{Change: "onto-binary-foundation", Workflow: "fix", Phase: "open"}
	if err := Save(filepath.Join(dir, "onto-state.yaml"), state); err != nil {
		t.Fatalf("Save returned unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proposal.md"), []byte("proposal"), 0o644); err != nil {
		t.Fatalf("failed to write proposal.md: %v", err)
	}

	err := ValidateSkeleton(dir)
	if err == nil {
		t.Fatal("ValidateSkeleton returned nil error for missing tasks.md, want error")
	}
	if !strings.Contains(err.Error(), "tasks.md") {
		t.Errorf("ValidateSkeleton error = %q, want it to contain %q", err.Error(), "tasks.md")
	}
}

func TestRequiredArtifacts_CumulativePerPhaseAndWorkflow(t *testing.T) {
	full := []string{"onto-state.yaml", "proposal.md"}
	preset := []string{"onto-state.yaml", "proposal.md", "tasks.md"}
	cases := []struct {
		phase, workflow string
		want            []string
	}{
		// full: tasks.md gates the design exit, not open; design.md + plan.md apply.
		{"open", "full", full},
		{"design", "full", append(append([]string{}, full...), "tasks.md", "design.md")},
		{"build", "full", append(append([]string{}, full...), "tasks.md", "design.md", "plan.md")},
		{"verify", "full", append(append([]string{}, full...), "tasks.md", "design.md", "plan.md", "verification.md")},
		{"close", "full", append(append([]string{}, full...), "tasks.md", "design.md", "plan.md", "verification.md")},
		{"bogus", "full", full},
		{"open", "", full}, // empty → strictest (full)
		// presets: tasks.md from open on; no design.md/plan.md; verification at verify.
		{"open", "fix", preset},
		{"design", "fix", preset},
		{"build", "tweak", preset},
		{"verify", "tweak", append(append([]string{}, preset...), "verification.md")},
		{"close", "fix", append(append([]string{}, preset...), "verification.md")},
	}

	for _, tc := range cases {
		t.Run(tc.phase+"/"+tc.workflow, func(t *testing.T) {
			got := RequiredArtifacts(tc.phase, tc.workflow)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RequiredArtifacts(%q, %q) = %v, want %v", tc.phase, tc.workflow, got, tc.want)
			}
		})
	}
}

func TestRequiredArtifacts_ReturnsFreshSliceEachCall(t *testing.T) {
	got1 := RequiredArtifacts("design", "full")
	got1[0] = "mutated"
	got2 := RequiredArtifacts("design", "full")
	if got2[0] == "mutated" {
		t.Error("RequiredArtifacts returned a shared slice; mutation leaked across calls")
	}
}

func TestNextPhase_WalksOrderedPhaseSequence(t *testing.T) {
	cases := []struct {
		phase    string
		wantNext string
		wantOK   bool
	}{
		{"open", "design", true},
		{"design", "build", true},
		{"build", "verify", true},
		{"verify", "close", true},
		{"close", "", false},
		{"bogus", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			next, ok := NextPhase(tc.phase)
			if next != tc.wantNext || ok != tc.wantOK {
				t.Errorf("NextPhase(%q) = (%q, %v), want (%q, %v)", tc.phase, next, ok, tc.wantNext, tc.wantOK)
			}
		})
	}
}

func TestTasksAllChecked_AllChecked_ReturnsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	content := "# Tasks\n- [x] one\n  - [x] two\n- [X] three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	got, err := TasksAllChecked(path)
	if err != nil {
		t.Fatalf("TasksAllChecked returned unexpected error: %v", err)
	}
	if !got {
		t.Error("TasksAllChecked = false, want true")
	}
}

func TestTasksAllChecked_OneUnchecked_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	content := "# Tasks\n- [x] one\n- [ ] two\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	got, err := TasksAllChecked(path)
	if err != nil {
		t.Fatalf("TasksAllChecked returned unexpected error: %v", err)
	}
	if got {
		t.Error("TasksAllChecked = true, want false")
	}
}

func TestTasksAllChecked_NoCheckboxes_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	content := "# Tasks\nNo checkboxes here.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	got, err := TasksAllChecked(path)
	if err != nil {
		t.Fatalf("TasksAllChecked returned unexpected error: %v", err)
	}
	if got {
		t.Error("TasksAllChecked = true, want false")
	}
}

func TestTasksAllChecked_MissingFile_ReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "tasks.md")

	_, err := TasksAllChecked(missing)
	if err == nil {
		t.Fatal("TasksAllChecked returned nil error for missing file, want error")
	}
}

func TestDepsResolved_OneArchivedOneMissing_ReturnsMissingOnly(t *testing.T) {
	root := t.TempDir()
	archiveDir := filepath.Join(root, "docs", "changes", "archive", "2026-07-10-a")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("failed to create fixture archive dir: %v", err)
	}

	got := DepsResolved(root, []string{"a", "b"})
	want := []string{"b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DepsResolved(root, [a,b]) = %v, want %v", got, want)
	}
}

func TestDepsResolved_NilDeps_ReturnsEmptySlice(t *testing.T) {
	root := t.TempDir()

	got := DepsResolved(root, nil)
	if len(got) != 0 {
		t.Errorf("DepsResolved(root, nil) = %v, want empty slice", got)
	}
}

func TestDepsResolved_EmptyDeps_ReturnsEmptySlice(t *testing.T) {
	root := t.TempDir()

	got := DepsResolved(root, []string{})
	if len(got) != 0 {
		t.Errorf("DepsResolved(root, []) = %v, want empty slice", got)
	}
}

func TestDepsResolved_PrefixCollision_DoesNotResolveShorterDep(t *testing.T) {
	root := t.TempDir()
	archiveDir := filepath.Join(root, "docs", "changes", "archive", "2026-07-10-ab")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("failed to create fixture archive dir: %v", err)
	}

	got := DepsResolved(root, []string{"a"})
	want := []string{"a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DepsResolved(root, [a]) = %v, want %v (ab archive must not resolve dep a)", got, want)
	}
}

// An active workspace with the dep's name overrides any archive hit: a reused
// name in flight is not archived. This is the documented contract
// (onto/SKILL.md §2 Dependencies) that was not enforced. See F10.
func TestDepsResolved_ActiveWorkspaceOverridesArchiveHit(t *testing.T) {
	root := t.TempDir()
	// Archived copy under archive/<date>-x
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes", "archive", "2026-07-10-x"), 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	// An ACTIVE workspace with the same name exists — the dep is NOT resolved.
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes", "x"), 0o755); err != nil {
		t.Fatalf("mkdir active: %v", err)
	}
	got := DepsResolved(root, []string{"x"})
	want := []string{"x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DepsResolved must NOT resolve when an active workspace of the same name exists; got %v, want %v", got, want)
	}
}

func TestDepsResolved_BypassArchiveDoesNotResolveDependency(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "docs", "changes", "archive", "2026-09-03-x")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	record := bypasslog.Record{At: "2026-09-03T12:00:00Z", Command: "onto bypass x --to archive --reason \"test\"", From: "open", To: "archive", Reason: "test", Skipped: []string{"merge"}}
	if err := bypasslog.Append(archive, "x", "onto", record); err != nil {
		t.Fatal(err)
	}
	if got := DepsResolved(root, []string{"x"}); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("DepsResolved bypass archive = %v, want unresolved x", got)
	}
}

func fullFixtureState() State {
	return State{
		SchemaVersion: CurrentSchemaVersion,
		Change:        "onto-binary-authoritative-state",
		Workflow:      "full",
		Phase:         "verify",
		Created:       "2026-07-13",
		BaseRef:       "cad5274",
		Deps:          []string{"onto-binary-foundation"},
		Isolation:     "worktree",
		BuildMode:     "subagent",
		TDDMode:       "tdd",
		Verify:        Verify{Scale: "full", Result: "pass"},
		Close:         Close{Merged: true},
		Directive:     "user said: ship it without asking again",
		Guides:        "updated",
		Archived:      false,
		Observed: Observed{
			Metrics:         map[string]string{"open": "2026-07-13", "build": "2026-07-13"},
			TasksTotal:      9,
			VerifyRounds:    2,
			PresetEscalated: true,
		},
	}
}

func TestMarshalParse_RoundTrip_PreservesEveryGatedField(t *testing.T) {
	want := fullFixtureState()
	b, err := Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestSave_StampsCurrentSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onto-state.yaml")
	if err := Save(path, State{Change: "x", Phase: "open"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestValidate_MalformedEnum_Rejected(t *testing.T) {
	cases := map[string]State{
		"isolation":     {Change: "c", Phase: "open", Isolation: "vm"},
		"build_mode":    {Change: "c", Phase: "open", BuildMode: "manual"},
		"tdd_mode":      {Change: "c", Phase: "open", TDDMode: "maybe"},
		"verify.scale":  {Change: "c", Phase: "open", Verify: Verify{Scale: "medium"}},
		"verify.result": {Change: "c", Phase: "open", Verify: Verify{Result: "green"}},
		"workflow":      {Change: "c", Phase: "open", Workflow: "epic"},
	}
	for name, st := range cases {
		if err := st.Validate(); err == nil {
			t.Errorf("Validate() accepted malformed %s, want error", name)
		}
	}
}

func TestValidate_EmptyOptionalEnums_Accepted(t *testing.T) {
	st := State{Change: "c", Phase: "open"}
	if err := st.Validate(); err != nil {
		t.Errorf("Validate() rejected empty optionals: %v", err)
	}
}

func TestValidate_Guides_AcceptsAllowedShapes(t *testing.T) {
	for _, g := range []string{"", "pending", "updated", "waived: no user-facing surface"} {
		st := State{Change: "c", Phase: "close", Guides: g}
		if err := st.Validate(); err != nil {
			t.Errorf("Validate() with guides %q = %v, want nil", g, err)
		}
	}
}

func TestValidate_Guides_RejectsUnknown(t *testing.T) {
	st := State{Change: "c", Phase: "close", Guides: "done"}
	if err := st.Validate(); err == nil {
		t.Fatal("Validate() with guides \"done\" = nil, want error")
	}
}

func TestValidGuides_Shapes(t *testing.T) {
	ok := []string{"", "pending", "updated", "waived: deferred to N7", "waived:x"}
	for _, v := range ok {
		if !ValidGuides(v) {
			t.Errorf("ValidGuides(%q) = false, want true", v)
		}
	}
	bad := []string{"done", "waived", "waived:", "waived:   "} // empty/whitespace reason rejected
	for _, v := range bad {
		if ValidGuides(v) {
			t.Errorf("ValidGuides(%q) = true, want false", v)
		}
	}
}

// Save must not follow a symlink planted at the destination, and must not leave
// a predictable .tmp name behind. See F8.
func TestSave_RefusesSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	// A victim file outside the state directory the attacker wants overwritten.
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	statePath := filepath.Join(dir, "onto-state.yaml")
	// Plant a symlink at the destination pointing at the victim.
	if err := os.Symlink(victim, statePath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := Save(statePath, State{Change: "c", Phase: "open"}); err == nil {
		t.Fatalf("Save must refuse to write through a planted symlink")
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if string(got) != "precious" {
		t.Errorf("victim was overwritten through the symlink: %q", string(got))
	}
}

// Save must not use a predictable temp filename that two writers could collide on.
// A leftover .homonto-* name is fine; a fixed onto-state.yaml.tmp is not.
func TestSave_LeavesNoPredictableTempName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onto-state.yaml")
	if err := Save(path, State{Change: "c", Phase: "open"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("predictable temp %s.tmp must not be used; stat err = %v", path, err)
	}
}

// TestEvidenceTokens_RoundTripAndAbsentDecode: the three judgment-gate
// evidence tokens survive a Marshal/Parse round-trip, and a state written
// before they existed decodes with all three empty (no schema bump).
func TestEvidenceTokens_RoundTripAndAbsentDecode(t *testing.T) {
	s := State{
		Change: "c", Phase: "design", Workflow: "full",
		ProposalApproved:  "2026-07-22 proposal ok",
		ApproachConfirmed: "2026-07-22 approach B",
		CloseConfirmed:    "2026-07-22 close plan shown",
	}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProposalApproved != s.ProposalApproved ||
		got.ApproachConfirmed != s.ApproachConfirmed ||
		got.CloseConfirmed != s.CloseConfirmed {
		t.Fatalf("round-trip lost evidence tokens: %+v", got)
	}

	old, err := Parse([]byte("change: c\nphase: open\n"))
	if err != nil {
		t.Fatal(err)
	}
	if old.ProposalApproved != "" || old.ApproachConfirmed != "" || old.CloseConfirmed != "" {
		t.Fatalf("legacy state must decode with empty tokens: %+v", old)
	}
}
