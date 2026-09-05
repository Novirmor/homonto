package ontocli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// seedEvidenceChange creates a change with a delta spec carrying stable IDs
// and a tasks.md, inside a git repository (evidence records anchor commits).
func runOntoErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func seedEvidenceChange(t *testing.T, root, name string) string {
	t.Helper()
	changeDir := filepath.Join(root, "docs", "changes", name)
	os.MkdirAll(filepath.Join(changeDir, "specs"), 0o755)
	ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"),
		ontostate.State{Change: name, Workflow: "full", Phase: "verify"})
	os.WriteFile(filepath.Join(changeDir, "proposal.md"), []byte("# p\n"), 0o644)
	os.WriteFile(filepath.Join(changeDir, "tasks.md"), []byte("# Tasks\n\n- [x] #1 implement\n- [ ] #2 verify\n"), 0o644)
	os.WriteFile(filepath.Join(changeDir, "verification.md"), []byte("# Verification\n\nResult: pending\n"), 0o644)
	os.WriteFile(filepath.Join(changeDir, "specs", "login.md"), []byte(`# Delta Spec: login (ev)

## ADDED Requirements

### Requirement: password reset

Requirement-ID: REQ-reset-1
The system SHALL email a reset link.

#### Scenario: expired token

Scenario-ID: SC-reset-expired
- **GIVEN** a token older than 1h
- **WHEN** the link is used
- **THEN** reset is refused
`), 0o644)
	return changeDir
}

func seedGitRepo(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("add", "-A")
	run("commit", "-qm", "seed")
}

const cmdHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestEvidenceRecordAndTrace: recording writes the sidecar (hashes, commit,
// operation), trace joins scenario -> evidence -> commit -> task, and `onto
// graph` output is untouched.
func TestEvidenceRecordAndTrace(t *testing.T) {
	root := prepWorkspace(t)
	seedEvidenceChange(t, root, "ev")
	seedGitRepo(t, root)

	os.WriteFile(filepath.Join(root, "out.txt"), []byte("PASS\n"), 0o644)
	out, _ := runOnto(t, "evidence", "record", "ev", "--dir", root,
		"--task", "2", "--scenario", "SC-reset-expired", "--exec", "go",
		"--cmd-hash", cmdHash, "--exit", "0", "--output", filepath.Join(root, "out.txt"))
	if !strings.Contains(out, "recorded SC-reset-expired") {
		t.Fatalf("record output: %q", out)
	}

	// The sidecar exists and carries hashes only.
	scData, err := os.ReadFile(filepath.Join(root, "docs", "changes", "ev", ".onto", "evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scData), `"schemaVersion": 1`) || strings.Contains(string(scData), "go test") {
		t.Fatalf("sidecar wrong:\n%s", scData)
	}

	// Trace joins the graph.
	tout, _ := runOnto(t, "trace", "ev", "--dir", root, "--json")
	var g struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal([]byte(tout), &g); err != nil {
		t.Fatalf("trace json: %v\n%s", err, tout)
	}
	kinds := map[string]bool{}
	for _, n := range g.Nodes {
		kinds[n["kind"].(string)] = true
	}
	for _, want := range []string{"change", "capability", "requirement", "scenario", "task", "evidence", "commit"} {
		if !kinds[want] {
			t.Errorf("trace missing %s node:\n%s", want, tout)
		}
	}
	joined := false
	for _, e := range g.Edges {
		if e["kind"] == "verified-by" && strings.HasPrefix(e["from"].(string), "scenario:") {
			joined = true
		}
	}
	if !joined {
		t.Errorf("scenario -> evidence edge missing:\n%s", tout)
	}

	// `onto graph` (the dependency view) still answers.
	gout, _ := runOnto(t, "graph", "--dir", root, "--json")
	if !strings.Contains(gout, `"nodes"`) {
		t.Fatalf("onto graph output changed:\n%s", gout)
	}
}

func TestEvidenceRecordWaitsForOntoWorkspaceLock(t *testing.T) {
	root := prepWorkspace(t)
	seedEvidenceChange(t, root, "ev")
	seedGitRepo(t, root)
	unlock, err := lockOnto(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = runOnto(t, "evidence", "record", "ev", "--dir", root,
		"--task", "2", "--scenario", "SC-reset-expired", "--exec", "go",
		"--cmd-hash", cmdHash)
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("record while locked = %v, want lock error", err)
	}
}

// TestEvidenceRecordRefusals: a record missing its inputs, or naming an
// unknown change, fails; nothing is written.
func TestEvidenceRecordRefusals(t *testing.T) {
	root := prepWorkspace(t)
	seedEvidenceChange(t, root, "ev")
	seedGitRepo(t, root)

	if _, err := runOntoErr(t, "evidence", "record", "ev", "--dir", root, "--task", "0", "--scenario", "x", "--exec", "go", "--cmd-hash", cmdHash); err == nil {
		t.Fatal("task 0 must fail")
	}
	if _, err := runOntoErr(t, "evidence", "record", "ev", "--dir", root, "--task", "1", "--scenario", "x", "--exec", "go", "--cmd-hash", "nothex"); err == nil {
		t.Fatal("bad cmd-hash must fail")
	}
	if _, err := runOntoErr(t, "evidence", "record", "ghost", "--dir", root, "--task", "1", "--scenario", "x", "--exec", "go", "--cmd-hash", cmdHash); err == nil {
		t.Fatal("unknown change must fail")
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "changes", "ev", ".onto")); err == nil {
		t.Fatal("refused records must write nothing")
	}
}

// TestEvidenceDoctorFindings: doctor reports unknown scenarios and tasks,
// unreachable commits, changed artifacts, duplicate IDs — and treats a
// sidecar-less change as healthy.
func TestEvidenceDoctorFindings(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := seedEvidenceChange(t, root, "ev")
	seedGitRepo(t, root)

	// A healthy record for an existing scenario/task.
	runOnto(t, "evidence", "record", "ev", "--dir", root,
		"--task", "2", "--scenario", "SC-reset-expired", "--exec", "go",
		"--cmd-hash", cmdHash, "--exit", "0")
	// A stale record: unknown scenario, unknown task.
	runOnto(t, "evidence", "record", "ev", "--dir", root,
		"--task", "9", "--scenario", "SC-ghost", "--exec", "go",
		"--cmd-hash", cmdHash, "--exit", "1")
	// A duplicate requirement ID in a second spec.
	os.WriteFile(filepath.Join(changeDir, "specs", "other.md"), []byte(
		"## ADDED Requirements\n\n### Requirement: another\n\nRequirement-ID: REQ-reset-1\nMUST x.\n"), 0o644)
	// verification.md changes after the records were taken.
	os.WriteFile(filepath.Join(changeDir, "verification.md"), []byte("# Verification\n\nResult: pass\n"), 0o644)

	out, err := runOntoErr(t, "doctor", "--dir", root)
	if err == nil {
		t.Fatalf("stale evidence must be findings:\n%s", out)
	}
	for _, want := range []string{"SC-ghost", "task #9", "duplicate Requirement-ID", "verification.md changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}

	// Unreachable commit: rewrite the sidecar with a bogus commit, in a repo
	// where git exists.
	scPath := filepath.Join(changeDir, ".onto", "evidence.json")
	data, _ := os.ReadFile(scPath)
	os.WriteFile(scPath, []byte(strings.Replace(string(data), `"commit": "`, `"commit": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef`, 1)), 0o644)
	out2, _ := runOntoErr(t, "doctor", "--dir", root)
	if !strings.Contains(out2, "unreachable") {
		t.Errorf("doctor missing unreachable-commit finding:\n%s", out2)
	}

	// A sidecar-less change (no verification.md) stays healthy on the evidence
	// axis.
	root2 := prepWorkspace(t)
	seedEvidenceChange(t, root2, "plain")
	os.Remove(filepath.Join(root2, "docs", "changes", "plain", "verification.md"))
	out3, _ := runOnto(t, "doctor", "--dir", root2)
	if strings.Contains(out3, "sidecar") || strings.Contains(out3, "evidence") {
		t.Errorf("sidecar-less change must be healthy on evidence:\n%s", out3)
	}
}

// TestEvidenceSidecarAttackRefusal: a planted destination symlink, a foreign
// regular file, and a symlinked .onto parent each fail without touching the
// target.
func TestEvidenceSidecarAttackRefusal(t *testing.T) {
	root := prepWorkspace(t)
	seedEvidenceChange(t, root, "ev")
	seedGitRepo(t, root)

	// Foreign regular file at the sidecar path.
	changeDir := filepath.Join(root, "docs", "changes", "ev")
	os.MkdirAll(filepath.Join(changeDir, ".onto"), 0o755)
	os.WriteFile(filepath.Join(changeDir, ".onto", "evidence.json"), []byte("not json"), 0o644)
	if _, err := runOntoErr(t, "evidence", "record", "ev", "--dir", root,
		"--task", "2", "--scenario", "SC-reset-expired", "--exec", "go",
		"--cmd-hash", cmdHash, "--exit", "0"); err == nil {
		t.Fatal("foreign file must be refused")
	}

	// Symlinked destination.
	root2 := prepWorkspace(t)
	cd2 := seedEvidenceChange(t, root2, "ev")
	seedGitRepo(t, root2)
	target := filepath.Join(root2, "secret.json")
	os.WriteFile(target, []byte("keep"), 0o644)
	os.MkdirAll(filepath.Join(cd2, ".onto"), 0o755)
	os.Symlink(target, filepath.Join(cd2, ".onto", "evidence.json"))
	if _, err := runOntoErr(t, "evidence", "record", "ev", "--dir", root2,
		"--task", "2", "--scenario", "SC-reset-expired", "--exec", "go",
		"--cmd-hash", cmdHash, "--exit", "0"); err == nil {
		t.Fatal("symlinked destination must be refused")
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Fatalf("symlink target was touched: %q %v", b, err)
	}

	// Symlinked parent.
	root3 := prepWorkspace(t)
	cd3 := seedEvidenceChange(t, root3, "ev")
	seedGitRepo(t, root3)
	escape := t.TempDir()
	os.Symlink(escape, filepath.Join(cd3, ".onto"))
	if _, err := runOntoErr(t, "evidence", "record", "ev", "--dir", root3,
		"--task", "2", "--scenario", "SC-reset-expired", "--exec", "go",
		"--cmd-hash", cmdHash, "--exit", "0"); err == nil {
		t.Fatal("symlinked parent must be refused")
	}
}
