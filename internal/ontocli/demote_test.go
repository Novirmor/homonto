package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tocli"
)

// toRun drives the `to` binary's command surface from these tests (tocli
// does not import ontocli, so there is no cycle). The promote→demote
// round-trip is inherently cross-binary.
func toRun(t *testing.T, wantErr bool, args ...string) string {
	t.Helper()
	cmd := tocli.NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if wantErr && err == nil {
		t.Fatalf("to execute %v = nil, want error; output: %s", args, out.String())
	}
	if !wantErr && err != nil {
		t.Fatalf("to execute %v: %v\noutput: %s", args, err, out.String())
	}
	return out.String()
}

// seedDemotable creates an onto change at the requested phase with proposal
// and (optionally) a task list in the canonical dotted+trace form.
func seedDemotable(t *testing.T, dir, name, phase, tasks string) {
	t.Helper()
	if _, err := run(t, "new", name, "--dir", dir); err != nil {
		t.Fatalf("new %s: %v", name, err)
	}
	stPath := filepath.Join(dir, "docs", "changes", name, "onto-state.yaml")
	if phase != "open" {
		st, err := ontostate.Load(stPath)
		if err != nil {
			t.Fatal(err)
		}
		st.Phase = phase
		if err := ontostate.Save(stPath, st); err != nil {
			t.Fatal(err)
		}
	}
	if tasks != "" {
		writeFile(t, filepath.Join(dir, "docs", "changes", name, "tasks.md"), tasks)
	}
}

const canonicalTasks = "# Tasks\n\n- [ ] 1.1 ship the parser [trace #1]\n- [x] 1.2 pin the fixtures [trace #2]\n"

// runFail executes the root command and returns the error text; the command
// must fail.
func runFail(t *testing.T, args ...string) string {
	t.Helper()
	_, err := run(t, args...)
	if err == nil {
		t.Fatalf("execute %v = nil, want error", args)
	}
	return err.Error()
}

// TestDemotePhaseAwareMapping: open/design demote to phase plan (nothing
// claimable); build with translatable tasks continues at phase do with a
// doctor-clean carried plan; build without parseable tasks restarts at plan.
func TestDemotePhaseAwareMapping(t *testing.T) {
	for _, tc := range []struct {
		phase string
		tasks string
		want  string
	}{
		{"open", canonicalTasks, "plan"},
		{"design", canonicalTasks, "plan"},
		{"build", canonicalTasks, "do"},
		{"verify", canonicalTasks, "do"},
		{"build", "# Tasks\n\nnothing parseable\n", "plan"},
	} {
		dir := setUpGatedWorkspace(t)
		seedDemotable(t, dir, "shrinker", tc.phase, tc.tasks)
		if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil {
			t.Fatalf("demote(%s): %v", tc.phase, err)
		}
		st := readFile(t, filepath.Join(dir, "docs", "tasks", "shrinker", "to-state.yaml"))
		if !strings.Contains(st, "phase: "+tc.want) {
			t.Fatalf("demote(%s) phase = %q, want %q (state: %q)", tc.phase, st, tc.want, st)
		}
		if tc.want == "do" {
			plan := readFile(t, filepath.Join(dir, "docs", "tasks", "shrinker", "plan.md"))
			for _, needle := range []string{"- [ ] #1 ship the parser", "- [x] #2 pin the fixtures", "Final Verify:"} {
				if !strings.Contains(plan, needle) {
					t.Fatalf("translated plan missing %q:\n%s", needle, plan)
				}
			}
		}
	}
}

// TestDemoteCreatesToWorkspace: demotion moves the source whole into the
// neutral control plane, generates the fresh workspace, and prints the
// complementary next steps.
func TestDemoteCreatesToWorkspace(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)

	out, err := run(t, "demote", "shrinker", "--dir", dir, "--yes")
	if err != nil {
		t.Fatalf("demote: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "demoted shrinker") || !strings.Contains(out, "complementary") {
		t.Fatalf("demote output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "shrinker")); err == nil {
		t.Fatal("source workspace still under docs/changes")
	}
	toDir := filepath.Join(dir, "docs", "tasks", "shrinker")
	for _, f := range []string{"to-state.yaml", "plan.md"} {
		if _, err := os.Stat(filepath.Join(toDir, f)); err != nil {
			t.Fatalf("demoted change missing %s: %v", f, err)
		}
	}
	// The snapshotted onto state is byte-identical history.
	snapOnto := findFile(t, filepath.Join(toDir, ".workflow", "snapshots"), "onto-state.yaml")
	if !strings.Contains(readFile(t, snapOnto), "change: shrinker") {
		t.Fatal("snapshotted onto state must keep the original bytes")
	}
	if !strings.Contains(readFile(t, filepath.Join(toDir, ".workflow", "lineage.json")), "\"currentWorkflow\": \"to\"") {
		t.Fatal("lineage must name to as current")
	}
}

// TestDemoteRequiresYesRefusesCollisionsAndTerminals: --yes is required;
// an existing target is refused; closed, abandoned, and unknown sources are
// refused — and no staging survives any refusal.
func TestDemoteRequiresYesRefusesCollisionsAndTerminals(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)

	if msg := runFail(t, "demote", "shrinker", "--dir", dir); !strings.Contains(msg, "--yes") {
		t.Fatalf("demote without --yes must fail: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")

	// Target collision — including the to-reserved "archive".
	os.MkdirAll(filepath.Join(dir, "docs", "tasks", "shrinker"), 0o755)
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("existing target must be refused: %q", msg)
	}
	os.RemoveAll(filepath.Join(dir, "docs", "tasks", "shrinker"))
	if msg := runFail(t, "demote", "shrinker", "--as", "archive", "--dir", dir, "--yes"); !strings.Contains(msg, "reserved") {
		t.Fatalf("reserved target name must be refused: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")

	// Unknown source.
	if msg := runFail(t, "demote", "ghost", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must fail: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")

	// Closed source is terminal.
	stPath := filepath.Join(dir, "docs", "changes", "shrinker", "onto-state.yaml")
	st, _ := ontostate.Load(stPath)
	st.Phase = "close"
	ontostate.Save(stPath, st)
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "closed") {
		t.Fatalf("closed change must be refused: %q", msg)
	}

	// Abandoned source is terminal too.
	st, _ = ontostate.Load(stPath)
	st.Phase = "build"
	st.Abandoned = true
	ontostate.Save(stPath, st)
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "terminal") {
		t.Fatalf("abandoned change must be refused: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")
}

// TestDemoteStateIdentityMustMatch: a copied workspace whose state names
// another change is refused before anything moves.
func TestDemoteStateIdentityMustMatch(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", "")
	writeFile(t, filepath.Join(dir, "docs", "changes", "shrinker", "onto-state.yaml"), "change: impostor\nphase: open\n")
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "impostor") {
		t.Fatalf("state/directory mismatch must be refused: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")
}

// TestDemoteIdempotentRetryAndUnrelatedTarget: a completed demotion retries
// as a receipt-verified success; an unrelated existing target is a refusal,
// never a fake completion.
func TestDemoteIdempotentRetryAndUnrelatedTarget(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)
	if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil { // receipt-verified retry
		t.Fatal(err)
	}

	os.MkdirAll(filepath.Join(dir, "docs", "tasks", "bystander"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "tasks", "bystander", "to-state.yaml"), []byte("change: bystander\nphase: plan\n"), 0o644)
	if msg := runFail(t, "demote", "ghost", "--as", "bystander", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must be refused, got: %q", msg)
	}
	// A live onto source aimed at the occupied target is a collision.
	seedDemotable(t, dir, "alive", "open", "")
	if msg := runFail(t, "demote", "alive", "--as", "bystander", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("occupied target must be refused, got: %q", msg)
	}
}

// TestDemoteTamperedStagingRefused: staging whose snapshot does not hash to
// the manifest is refused.
func TestDemoteTamperedStagingRefused(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)

	base := filepath.Join(dir, "docs", ".onto-demote")
	os.MkdirAll(base, 0o755)
	stg := filepath.Join(base, "deadbeef")
	os.MkdirAll(filepath.Join(stg, "work", ".workflow", "snapshots", "deadbeef", "onto"), 0o755)
	os.WriteFile(filepath.Join(stg, "manifest.json"), []byte(`{"schemaVersion":1,"kind":"convert","direction":"demote","source":"shrinker","target":"shrinker","operationId":"deadbeef","lineage":{"schemaVersion":1,"lineageId":"lin","created":"2026-09-04","currentWorkflow":"to"},"sourcePhase":"open","targetIdentity":{"phase":"plan","created":"2026-09-04"},"sourceDigest":"sha256:x","sourceHashes":{"onto-state.yaml":"00"}}`), 0o644)

	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing") && !strings.Contains(msg, "tampered") {
		t.Fatalf("tampered staging must be refused: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "tasks", "shrinker")); err == nil {
		t.Fatal("refused recovery must not install a target")
	}
}

// TestPromoteThenDemoteRestores (ADR 0042): converting back with nothing
// changed restores the original `to` workspace byte-for-byte and appends a
// restore event; converting back AFTER an edit is a fresh conversion, not a
// restore.
func TestPromoteThenDemoteRestores(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	// Both frameworks declared and applied: complementarity is the premise.
	writeFile(t, filepath.Join(dir, "homonto.toml"),
		"[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n[frameworks.to]\nsource=\"builtin:to\"\nscope=\"project\"\n")
	os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755)

	toRun(t, false, "new", "grower", "--dir", dir)
	toRun(t, false, "phase", "grower", "--dir", dir)
	planPath := filepath.Join(dir, "docs", "tasks", "grower", "plan.md")
	os.WriteFile(planPath, []byte("# plan\n- [ ] #1 the work\n  - Files: `x.go`\n  - Change: the work\n  - Verify: `go test ./...`\nFinal Verify: `go test ./...`\n"), 0o644)
	originalPlan := readFile(t, planPath)

	toRun(t, false, "promote", "grower", "--dir", dir, "--yes")
	if _, err := run(t, "demote", "grower", "--dir", dir, "--yes"); err != nil {
		t.Fatalf("demote after promote: %v", err)
	}

	// The original workspace is back: phase do, identical plan bytes.
	if st := readFile(t, filepath.Join(dir, "docs", "tasks", "grower", "to-state.yaml")); !strings.Contains(st, "phase: do") {
		t.Fatalf("restored state must be the original do phase: %q", st)
	}
	if got := readFile(t, planPath); got != originalPlan {
		t.Fatalf("restored plan differs:\n got: %q\nwant: %q", got, originalPlan)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower")); err == nil {
		t.Fatal("demoted onto workspace still present")
	}
	lin := readFile(t, filepath.Join(dir, "docs", "tasks", "grower", ".workflow", "lineage.json"))
	if !strings.Contains(lin, "\"currentWorkflow\": \"to\"") {
		t.Fatalf("lineage must name to after restore: %s", lin)
	}
	if events := listDirEntries(t, filepath.Join(dir, "docs", "tasks", "grower", ".workflow", "events")); len(events) != 2 {
		t.Fatalf("promote + restore events expected, got %v", events)
	}

	// A second round trip after an edit is a fresh conversion: promote
	// (fresh, since the restore discarded the onto bytes), edit the onto
	// proposal, then demote — the edited onto bytes are snapshotted, not
	// discarded.
	toRun(t, false, "promote", "grower", "--dir", dir, "--yes")
	writeFile(t, filepath.Join(dir, "docs", "changes", "grower", "proposal.md"), "# Proposal: grower\n\nedited after promotion\n")
	if _, err := run(t, "demote", "grower", "--dir", dir, "--yes"); err != nil {
		t.Fatalf("demote after edit: %v", err)
	}
	snapProposal := findFile(t, filepath.Join(dir, "docs", "tasks", "grower", ".workflow", "snapshots"), "proposal.md")
	if got := readFile(t, snapProposal); !strings.Contains(got, "edited after promotion") {
		t.Fatalf("edited source must be snapshotted, got %q", got)
	}
	// The edited demote is a normal conversion: phase comes from mapping
	// (open -> plan), not a restore to do.
	if st := readFile(t, filepath.Join(dir, "docs", "tasks", "grower", "to-state.yaml")); !strings.Contains(st, "phase: plan") {
		t.Fatalf("edited round trip must map open -> plan, got %q", st)
	}
}

// TestDemoteAcceptsEitherFramework pins the bridge gate: demotion works with
// [frameworks.onto] applied (the change lives there) and with
// [frameworks.to] applied (the destination framework, ADR 0042).
func TestDemoteAcceptsEitherFramework(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", "")
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.to]\nsource=\"builtin:to\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil {
		t.Fatalf("demote under to-applied config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "tasks", "shrinker", "to-state.yaml")); err != nil {
		t.Fatalf("to state missing: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no %s under %s", name, root)
	}
	return found
}

func listDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertNoStaging(t *testing.T, dir, name string) {
	t.Helper()
	if entries := listDirEntries(t, filepath.Join(dir, "docs", name)); len(entries) != 0 {
		t.Fatalf("failed precondition must leave no %s staging, got %v", name, entries)
	}
}
