package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const deltaDoc = `# Delta Spec: login (feat-a)

## ADDED Requirements

### Requirement: password reset

Requirement-ID: REQ-reset-1
The system SHALL email a reset link.

#### Scenario: expired token

Scenario-ID: SC-reset-expired
- **GIVEN** a token older than 1h
- **WHEN** the link is used
- **THEN** reset is refused

#### Scenario: happy path

- **GIVEN** a valid token
- **WHEN** the link is used
- **THEN** a new password is set

## MODIFIED Requirements

### Requirement: login rate limit

The system MUST throttle after 5 failures.
`

func TestParseRequirements(t *testing.T) {
	reqs := ParseRequirements(deltaDoc)
	if len(reqs) != 2 {
		t.Fatalf("want 2 requirements, got %d", len(reqs))
	}
	r := reqs[0]
	if r.Name != "password reset" || r.ID != "REQ-reset-1" || r.Section != "ADDED" {
		t.Fatalf("first requirement wrong: %+v", r)
	}
	if len(r.Scenarios) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(r.Scenarios))
	}
	if r.Scenarios[0].Name != "expired token" || r.Scenarios[0].ID != "SC-reset-expired" {
		t.Fatalf("scenario ID wrong: %+v", r.Scenarios[0])
	}
	if r.Scenarios[1].ID != "" {
		t.Fatalf("scenario without ID must parse empty: %+v", r.Scenarios[1])
	}
	if reqs[1].Section != "MODIFIED" || reqs[1].ID != "" {
		t.Fatalf("modified requirement wrong: %+v", reqs[1])
	}
}

func TestParseTasks(t *testing.T) {
	tasks := ParseTasks("# Tasks\n\n- [ ] #1 legacy parser task\n- [x] 1.1 test it [trace #2]\n  - detail\n- [ ] 1.2 wire the command [trace #3]\n- [ ] 1.3 mention issue #4 without a trace marker\n- [ ] 1.4 malformed [trace #0]\n")
	if len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Checked || !tasks[1].Checked || tasks[2].Number != 3 {
		t.Fatalf("tasks wrong: %+v", tasks)
	}
}

func TestSidecarRoundTripAndForeignRefusal(t *testing.T) {
	dir := t.TempDir()
	sc := New("feat-a")
	sc.Records = append(sc.Records, Record{Task: 1, Scenario: "SC-reset-expired", Executable: "go", CommandHash: "aa", Commit: "c0ffee", ExitStatus: 0, OutputHash: "bb", ArtifactHash: "cc"})

	if err := Save(dir, sc); err != nil {
		t.Fatal(err)
	}
	back, ok, err := Load("feat-a", Path(dir))
	if err != nil || !ok {
		t.Fatalf("load: %v %v", ok, err)
	}
	if len(back.Records) != 1 || back.Records[0].Scenario != "SC-reset-expired" {
		t.Fatalf("round trip lost data: %+v", back)
	}

	// Wrong change: refused.
	if _, _, err := Load("feat-b", Path(dir)); err == nil {
		t.Fatal("change mismatch must fail")
	}
	// Foreign existing file: refused on save.
	foreign := t.TempDir()
	os.MkdirAll(filepath.Join(foreign, ".onto"), 0o755)
	os.WriteFile(Path(foreign), []byte(`{"unexpected": true}`), 0o644)
	if err := Save(foreign, New("x")); err == nil {
		t.Fatal("foreign file must be refused")
	}
	// Destination symlink: refused.
	linked := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "real.json")
	os.WriteFile(elsewhere, []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(linked, ".onto"), 0o755)
	os.Symlink(elsewhere, Path(linked))
	if err := Save(linked, New("x")); err == nil {
		t.Fatal("symlinked destination must be refused")
	}
	// Symlinked parent: refused.
	parent := t.TempDir()
	escape := t.TempDir()
	os.Symlink(escape, filepath.Join(parent, ".onto"))
	if err := Save(parent, New("x")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked parent must be refused: %v", err)
	}
	// Newer schema: refused.
	if err := ValidateSchema(SchemaVersion + 1); err == nil {
		t.Fatal("newer schema must be refused")
	}
	// Missing file: legacy, not an error.
	if _, ok, err := Load("feat-a", filepath.Join(t.TempDir(), "absent.json")); ok || err != nil {
		t.Fatalf("absent sidecar must be legacy: %v %v", ok, err)
	}
}
