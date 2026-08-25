package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var activateNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

// installation is a fake Homonto installation on disk.
type installation struct {
	root    string
	binary  string
	wrapper string
	service *Service
	active  bool
}

// candidateScript is a fake candidate binary that answers the metadata
// command with the given version.
func candidateScript(version string, schema int64) []byte {
	return []byte("#!/bin/sh\ncat <<'JSON'\n" +
		`{"version":"` + version + `","protocol_version":1,"store_schema_version":` +
		itoa(schema) + `}` + "\nJSON\n")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func newInstallation(t *testing.T) *installation {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the fake installation needs a POSIX shell")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "homonto")
	wrapper := filepath.Join(root, ".claude", "skills", "homonto-task", "SKILL.md")
	for _, path := range []string{binary, wrapper} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(binary, candidateScript("v1.0.0", 1), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(wrapper, []byte("# the old wrapper\n"), 0o644); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	inst := &installation{root: root, binary: binary, wrapper: wrapper}
	service, err := NewService(Options{
		ControlRoot: root, Binary: binary, Wrappers: []string{wrapper},
		ActiveWork: func(context.Context) (bool, error) { return inst.active, nil },
		Now:        func() time.Time { return activateNow },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	inst.service = service
	return inst
}

// localSchema is the schema this binary knows, so a fixture candidate is
// compatible for the reasons the test is about rather than incidentally
// incompatible on a version it never meant to vary.
func localSchema() int64 { return LocalMetadata().StoreSchemaVersion }

// stage writes a candidate and returns it.
func (i *installation) stage(t *testing.T, version string, schema int64) StagedGeneration {
	t.Helper()
	body := candidateScript(version, schema)
	release := VerifiedRelease{
		Manifest: Manifest{Version: version},
		Artifact: Artifact{
			OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://x.example/homonto",
			SHA256: digestOf(body), Size: int64(len(body)),
		},
	}
	staged, err := i.service.Stage(context.Background(), release, body)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	return staged
}

func (i *installation) read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func TestStageReplacesNothing(t *testing.T) {
	inst := newInstallation(t)
	before := inst.read(t, inst.binary)
	staged := inst.stage(t, "v2.0.0", localSchema())

	if inst.read(t, inst.binary) != before {
		t.Fatal("staging replaced the installed binary")
	}
	if staged.Metadata.Version != "v2.0.0" {
		t.Fatalf("metadata = %+v", staged.Metadata)
	}
	if _, err := os.Stat(staged.Path); err != nil {
		t.Fatalf("the candidate was not staged: %v", err)
	}
	// Discarding a staged candidate is deleting a directory, not undoing
	// an installation.
	if err := inst.service.DiscardStaged(staged); err != nil {
		t.Fatalf("DiscardStaged: %v", err)
	}
	if _, err := os.Stat(staged.Path); err == nil {
		t.Fatal("the discarded candidate is still staged")
	}
}

// TestStageRefusesACandidateThatIsNotWhatTheManifestSaid proves the
// candidate is checked against its own manifest.
func TestStageRefusesACandidateThatIsNotWhatTheManifestSaid(t *testing.T) {
	inst := newInstallation(t)
	body := candidateScript("v2.0.0", localSchema())
	release := VerifiedRelease{
		Manifest: Manifest{Version: "v3.0.0"}, // the manifest lies
		Artifact: Artifact{
			OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://x.example/homonto",
			SHA256: digestOf(body), Size: int64(len(body)),
		},
	}
	if _, err := inst.service.Stage(context.Background(), release, body); !errors.Is(err, ErrCandidateMismatch) {
		t.Fatalf("Stage error = %v, want ErrCandidateMismatch", err)
	}
	// And a body that does not match the checksum never reaches the disk.
	release.Manifest.Version = "v2.0.0"
	release.Artifact.SHA256 = digestOf([]byte("something else"))
	if _, err := inst.service.Stage(context.Background(), release, body); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Stage error = %v, want ErrChecksumMismatch", err)
	}
}

// TestActivationRefusesWhileWorkIsActive is the spec's rule: replacing the
// binary under a running workflow means the next `homonto next` is
// answered by a different program than the one that issued the actions.
func TestActivationRefusesWhileWorkIsActive(t *testing.T) {
	inst := newInstallation(t)
	staged := inst.stage(t, "v2.0.0", localSchema())
	inst.active = true

	before := inst.read(t, inst.binary)
	if err := inst.service.Activate(context.Background(), staged); !errors.Is(err, ErrWorkActive) {
		t.Fatalf("Activate error = %v, want ErrWorkActive", err)
	}
	if inst.read(t, inst.binary) != before {
		t.Fatal("a refused activation replaced the binary")
	}
	if _, err := ReadJournal(inst.service.Paths()); !errors.Is(err, ErrNoJournal) {
		t.Fatal("a refused activation left a journal")
	}
}

// TestAServiceThatCannotCheckRefuses proves the safe direction: a service
// with no way to tell whether work is active does not assume the answer
// it prefers.
func TestAServiceThatCannotCheckRefuses(t *testing.T) {
	inst := newInstallation(t)
	staged := inst.stage(t, "v2.0.0", localSchema())
	blind, err := NewService(Options{
		ControlRoot: inst.root, Binary: inst.binary,
		Now: func() time.Time { return activateNow },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := blind.Activate(context.Background(), staged); !errors.Is(err, ErrWorkActive) {
		t.Fatalf("Activate error = %v, want ErrWorkActive", err)
	}
}

func TestActivationRefusesAnIncompatibleCandidate(t *testing.T) {
	inst := newInstallation(t)
	// An older schema than the running binary knows.
	staged := inst.stage(t, "v2.0.0", localSchema()-1)
	if staged.Compatibility.OK() {
		t.Fatal("a candidate with an older schema was judged compatible")
	}
	before := inst.read(t, inst.binary)
	if err := inst.service.Activate(context.Background(), staged); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("Activate error = %v, want ErrIncompatible", err)
	}
	if inst.read(t, inst.binary) != before {
		t.Fatal("an incompatible candidate was installed")
	}
}

// TestActivationInstallsAndClearsItsJournal is the happy path.
func TestActivationInstallsAndClearsItsJournal(t *testing.T) {
	inst := newInstallation(t)
	staged := inst.stage(t, "v2.0.0", localSchema())
	staged.Compatibility = Compatibility{}

	if err := inst.service.Activate(context.Background(), staged); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !strings.Contains(inst.read(t, inst.binary), "v2.0.0") {
		t.Fatal("the candidate was not installed")
	}
	// The marker records the new generation.
	marker, err := ReadMarker(inst.service.Paths())
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if marker.Version != "v2.0.0" {
		t.Fatalf("marker = %+v", marker)
	}
	// A finished update leaves no journal, so ordinary commands run.
	if _, err := ReadJournal(inst.service.Paths()); !errors.Is(err, ErrNoJournal) {
		t.Fatalf("a finished activation left a journal: %v", err)
	}
	pending, err := Pending(inst.root)
	if err != nil || pending {
		t.Fatalf("Pending = %v, %v, want false", pending, err)
	}
	// The exact pre-update backup is retained.
	backup := filepath.Join(inst.service.Paths().BackupDir(staged.ID), string(StepBinary))
	if !strings.Contains(inst.read(t, backup), "v1.0.0") {
		t.Fatal("the exact pre-update binary was not retained")
	}
}

// TestCrashAtEveryBoundaryRecovers is the crash matrix. Each case
// truncates the journal at one step boundary and proves recovery lands on
// a coherent installation.
func TestCrashAtEveryBoundaryRecovers(t *testing.T) {
	cases := []struct {
		name string
		// applied names the steps that had committed before the crash.
		applied []StepKind
		// wantNew reports whether recovery should end on the new binary.
		wantNew bool
	}{
		{"before anything", nil, false},
		{"after the binary", []StepKind{StepBinary}, false},
		{"after the state migration", []StepKind{StepBinary, StepState}, false},
		{"after the wrappers", []StepKind{StepBinary, StepState, StepWrappers}, false},
		// Past the marker the activation has committed, so recovery
		// finishes forward rather than undoing a live installation.
		{"after the marker", []StepKind{StepBinary, StepState, StepWrappers, StepMarker}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := newInstallation(t)
			staged := inst.stage(t, "v2.0.0", localSchema())
			original := inst.read(t, inst.binary)
			originalWrapper := inst.read(t, inst.wrapper)

			// Replay the activation by hand up to the crash point.
			journal := Journal{
				SchemaVersion: JournalSchema, ID: staged.ID,
				From: Generation{Version: "v1.0.0"}, To: Generation{Version: "v2.0.0"},
				StartedAt: activateNow,
			}
			for _, kind := range order {
				journal.Steps = append(journal.Steps, Step{Kind: kind, State: StatePending})
			}
			paths := inst.service.Paths()
			for _, kind := range tc.applied {
				step := journal.step(kind)
				switch kind {
				case StepBinary:
					backup := filepath.Join(paths.BackupDir(staged.ID), string(StepBinary))
					existed, err := backupFile(inst.binary, backup)
					if err != nil {
						t.Fatalf("backup: %v", err)
					}
					step.Target, step.Backup, step.Existed = inst.binary, backup, existed
					body, err := os.ReadFile(staged.Path)
					if err != nil {
						t.Fatalf("read candidate: %v", err)
					}
					if err := writeFileAtomic(inst.binary, body, 0o755); err != nil {
						t.Fatalf("replace binary: %v", err)
					}
				case StepWrappers:
					backup := filepath.Join(paths.BackupDir(staged.ID), "wrapper-0")
					existed, err := backupFile(inst.wrapper, backup)
					if err != nil {
						t.Fatalf("backup: %v", err)
					}
					step.Target, step.Backup, step.Existed = inst.wrapper, backup, existed
					if err := writeFileAtomic(inst.wrapper, []byte("# the new wrapper\n"), 0o644); err != nil {
						t.Fatalf("replace wrapper: %v", err)
					}
				case StepMarker:
					step.Target = paths.MarkerPath()
					step.Backup = filepath.Join(paths.BackupDir(staged.ID), "generation.json")
					if err := WriteMarker(paths, journal.To); err != nil {
						t.Fatalf("write marker: %v", err)
					}
				}
				step.State = StateApplied
			}
			if err := WriteJournal(paths, journal); err != nil {
				t.Fatalf("WriteJournal: %v", err)
			}

			// An interrupted update blocks ordinary commands.
			pending, err := Pending(inst.root)
			if err != nil || !pending {
				t.Fatalf("Pending = %v, %v, want true", pending, err)
			}

			if err := inst.service.RecoverPending(context.Background()); err != nil {
				t.Fatalf("RecoverPending: %v", err)
			}
			if _, err := ReadJournal(paths); !errors.Is(err, ErrNoJournal) {
				t.Fatalf("recovery left a journal: %v", err)
			}

			got := inst.read(t, inst.binary)
			if tc.wantNew {
				if !strings.Contains(got, "v2.0.0") {
					t.Fatal("a committed activation was rolled back")
				}
				return
			}
			// Exact byte restoration, not "close enough".
			if got != original {
				t.Fatalf("the binary was not restored exactly:\n%q\nwant\n%q", got, original)
			}
			if inst.read(t, inst.wrapper) != originalWrapper {
				t.Fatal("the wrapper was not restored exactly")
			}
			if _, err := ReadMarker(paths); !errors.Is(err, ErrNoJournal) {
				t.Fatal("a rolled-back activation left a generation marker")
			}
		})
	}
}

// TestRollbackRemovesAFileThatNeverExisted proves a rollback rolls all the
// way back rather than leaving something the previous installation lacked.
func TestRollbackRemovesAFileThatNeverExisted(t *testing.T) {
	inst := newInstallation(t)
	staged := inst.stage(t, "v2.0.0", localSchema())
	newFile := filepath.Join(inst.root, "bin", "homonto-new")
	if err := writeFileAtomic(newFile, []byte("new\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	journal := Journal{
		SchemaVersion: JournalSchema, ID: staged.ID, StartedAt: activateNow,
		Steps: []Step{{Kind: StepBinary, Target: newFile, Existed: false, State: StateApplied}},
	}
	if err := WriteJournal(inst.service.Paths(), journal); err != nil {
		t.Fatalf("WriteJournal: %v", err)
	}
	if err := inst.service.Rollback(context.Background(), journal); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Stat(newFile); err == nil {
		t.Fatal("a file the previous installation never had survived the rollback")
	}
}

// TestAFailedRestoreLeavesTheJournal proves the one failure with no good
// answer is reported rather than tidied away.
func TestAFailedRestoreLeavesTheJournal(t *testing.T) {
	inst := newInstallation(t)
	journal := Journal{
		SchemaVersion: JournalSchema, ID: "x", StartedAt: activateNow,
		Steps: []Step{{
			Kind: StepBinary, Target: inst.binary,
			Backup: filepath.Join(inst.root, "missing-backup"), Existed: true, State: StateApplied,
		}},
	}
	if err := WriteJournal(inst.service.Paths(), journal); err != nil {
		t.Fatalf("WriteJournal: %v", err)
	}
	if err := inst.service.Rollback(context.Background(), journal); !errors.Is(err, ErrRestoreFailed) {
		t.Fatalf("Rollback error = %v, want ErrRestoreFailed", err)
	}
	if _, err := ReadJournal(inst.service.Paths()); err != nil {
		t.Fatalf("a failed restore cleared the journal: %v", err)
	}
	pending, err := Pending(inst.root)
	if err != nil || !pending {
		t.Fatalf("Pending = %v, %v, want true after a failed restore", pending, err)
	}
}

// TestActivationRollsBackOnFailure proves a mid-activation failure does
// not leave a half-installed binary.
func TestActivationRollsBackOnFailure(t *testing.T) {
	inst := newInstallation(t)
	staged := inst.stage(t, "v2.0.0", localSchema())
	staged.Compatibility = Compatibility{}
	original := inst.read(t, inst.binary)

	// Make the marker unwritable so activation fails after the binary is
	// already in place.
	paths := inst.service.Paths()
	if err := os.MkdirAll(paths.MarkerPath(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	err := inst.service.Activate(context.Background(), staged)
	if err == nil {
		t.Fatal("Activate succeeded despite an unwritable marker")
	}
	if inst.read(t, inst.binary) != original {
		t.Fatalf("a failed activation left the new binary in place:\n%q", inst.read(t, inst.binary))
	}
}

// TestAnUnreadableJournalBlocksOrdinaryCommands proves the worst case
// refuses rather than running as if nothing were underway.
func TestAnUnreadableJournalBlocksOrdinaryCommands(t *testing.T) {
	inst := newInstallation(t)
	paths := inst.service.Paths()
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(paths.JournalPath(), []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pending, err := Pending(inst.root)
	if !pending {
		t.Fatal("an unreadable journal did not block ordinary commands")
	}
	if !errors.Is(err, ErrJournalUnreadable) {
		t.Fatalf("error = %v, want ErrJournalUnreadable", err)
	}
}

// TestJournalRoundTripsAndValidates guards the format both binaries must
// understand.
func TestJournalRoundTripsAndValidates(t *testing.T) {
	inst := newInstallation(t)
	paths := inst.service.Paths()
	journal := Journal{
		SchemaVersion: JournalSchema, ID: "abc", StartedAt: activateNow,
		From: Generation{Version: "v1.0.0"}, To: Generation{Version: "v2.0.0"},
		Steps: []Step{{Kind: StepBinary, State: StatePending}},
	}
	if err := WriteJournal(paths, journal); err != nil {
		t.Fatalf("WriteJournal: %v", err)
	}
	got, err := ReadJournal(paths)
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if got.ID != "abc" || got.To.Version != "v2.0.0" {
		t.Fatalf("journal = %+v", got)
	}
	for _, bad := range []Journal{
		{SchemaVersion: 99, ID: "a", Steps: []Step{{Kind: StepBinary, State: StatePending}}},
		{SchemaVersion: JournalSchema, Steps: []Step{{Kind: StepBinary, State: StatePending}}},
		{SchemaVersion: JournalSchema, ID: "a"},
		{SchemaVersion: JournalSchema, ID: "a", Steps: []Step{{Kind: "sideways", State: StatePending}}},
		{SchemaVersion: JournalSchema, ID: "a", Steps: []Step{{Kind: StepBinary, State: "maybe"}}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("an invalid journal validated: %+v", bad)
		}
	}
}
