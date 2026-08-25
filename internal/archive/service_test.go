package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/securefs"
)

// day is the fixed archive date every test uses; archiving is
// date-sensitive and must never depend on the wall clock.
var day = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

// newService returns a service over a fresh control root that already has
// the active/ and archive/ directories the service never creates itself.
func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{ActiveDir, Dir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	svc, err := NewService(dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, dir
}

func mustWorkID(t *testing.T) identity.WorkID {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	return id
}

// writeTaskDoc creates a task document at active/<dirName>/tasks.md whose
// metadata declares name — the directory name and the declared name are
// separate on purpose, because identity never comes from the path.
func writeTaskDoc(t *testing.T, root string, workID identity.WorkID, dirName, name string) {
	t.Helper()
	doc := artifact.NewDocument(artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: workID, Name: name, Kind: artifact.KindTaskDocument,
	})
	doc.Regions = []artifact.RegionContent{
		{Region: artifact.RegionTaskGoal, Content: []byte("## Goal\nmake it work\n")},
		{Region: artifact.RegionTaskChecklist, Content: []byte("- [ ] item\n")},
		{Region: artifact.RegionTaskEvidence, Content: nil},
	}
	writeDoc(t, root, ActiveDir+"/"+dirName+"/tasks.md", doc)
}

// writeProposal creates a change proposal at active/<dirName>/proposal.md.
func writeProposal(t *testing.T, root string, workID identity.WorkID, dirName, name string) {
	t.Helper()
	doc := artifact.NewDocument(artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: workID, Name: name, Kind: artifact.KindProposal,
	})
	doc.Regions = []artifact.RegionContent{
		{Region: artifact.RegionWholeDocument, Content: []byte("## Proposal\nscope\n")},
	}
	writeDoc(t, root, ActiveDir+"/"+dirName+"/proposal.md", doc)
}

// writeDoc renders doc and writes it at the control-root-relative rel,
// creating the parent directory the service itself never creates.
func writeDoc(t *testing.T, root, rel string, doc artifact.Document) {
	t.Helper()
	b, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("Render %s: %v", rel, err)
	}
	writeFile(t, root, rel, b)
}

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// exists reports whether rel is present under the control root.
func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func TestNewServiceRejectsRelativeRoot(t *testing.T) {
	if _, err := NewService("relative/path"); err == nil {
		t.Fatal("NewService(relative) = nil error, want rejection")
	}
	if _, err := NewService(""); err == nil {
		t.Fatal("NewService(\"\") = nil error, want rejection")
	}
}

func TestArchiveTaskMovesFileIntoArchive(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	writeTaskDoc(t, root, workID, "fix-login", "fix-login")

	entry, err := svc.ArchiveTask(t.Context(), "active/fix-login/tasks.md", day)
	if err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	if entry.Path != "archive/2026-08-25-fix-login.md" {
		t.Fatalf("entry.Path = %q, want archive/2026-08-25-fix-login.md", entry.Path)
	}
	if entry.IsDir {
		t.Fatal("task archive entry must be a file")
	}
	if entry.Kind != artifact.KindTaskDocument || entry.WorkID != workID || entry.Name != "fix-login" {
		t.Fatalf("entry metadata mismatch: %+v", entry)
	}
	if entry.Date != "2026-08-25" {
		t.Fatalf("entry.Date = %q, want 2026-08-25", entry.Date)
	}
	if exists(t, root, "active/fix-login/tasks.md") {
		t.Fatal("source file still present after archive")
	}
	if !exists(t, root, "archive/2026-08-25-fix-login.md") {
		t.Fatal("archived file missing")
	}
}

// TestArchiveTaskNamesFromMetadataNotPath proves the archive name comes
// from the metadata block: the directory is called something else.
func TestArchiveTaskNamesFromMetadataNotPath(t *testing.T) {
	svc, root := newService(t)
	writeTaskDoc(t, root, mustWorkID(t), "some-scratch-dir", "fix-login")

	entry, err := svc.ArchiveTask(t.Context(), "active/some-scratch-dir/tasks.md", day)
	if err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	if entry.Path != "archive/2026-08-25-fix-login.md" {
		t.Fatalf("entry.Path = %q, want the metadata name, not the directory name", entry.Path)
	}
}

func TestArchiveTaskRefusesNonTaskDocument(t *testing.T) {
	svc, root := newService(t)
	writeProposal(t, root, mustWorkID(t), "rework-catalog", "rework-catalog")

	if _, err := svc.ArchiveTask(t.Context(), "active/rework-catalog/proposal.md", day); err == nil {
		t.Fatal("ArchiveTask of a proposal = nil error, want refusal")
	}
}

func TestArchiveTaskSameDayCollisionGetsSuffix(t *testing.T) {
	svc, root := newService(t)
	writeTaskDoc(t, root, mustWorkID(t), "first", "fix-login")

	e1, err := svc.ArchiveTask(t.Context(), "active/first/tasks.md", day)
	if err != nil {
		t.Fatalf("first ArchiveTask: %v", err)
	}
	if e1.Path != "archive/2026-08-25-fix-login.md" {
		t.Fatalf("first entry path = %q", e1.Path)
	}
	// A second, different work with the same name archived the same day
	// must land on -2 rather than overwrite the first.
	writeTaskDoc(t, root, mustWorkID(t), "second", "fix-login")
	e2, err := svc.ArchiveTask(t.Context(), "active/second/tasks.md", day)
	if err != nil {
		t.Fatalf("second ArchiveTask: %v", err)
	}
	if e2.Path != "archive/2026-08-25-fix-login-2.md" {
		t.Fatalf("second entry path = %q, want -2 suffix", e2.Path)
	}
	if !exists(t, root, "archive/2026-08-25-fix-login.md") {
		t.Fatal("first archive entry was overwritten")
	}
}

func TestArchiveChangeMovesDirectory(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	writeProposal(t, root, workID, "rework-catalog", "rework-catalog")
	// A change directory carries more than the proposal; archive must move
	// the whole directory.
	writeFile(t, root, "active/rework-catalog/design.md", []byte("## Design\n"))

	entry, err := svc.ArchiveChange(t.Context(), workID, day)
	if err != nil {
		t.Fatalf("ArchiveChange: %v", err)
	}
	if entry.Path != "archive/2026-08-25-rework-catalog" {
		t.Fatalf("entry.Path = %q", entry.Path)
	}
	if !entry.IsDir {
		t.Fatal("change archive entry must be a directory")
	}
	if entry.WorkID != workID || entry.Kind != artifact.KindProposal {
		t.Fatalf("entry metadata mismatch: %+v", entry)
	}
	for _, rel := range []string{
		"archive/2026-08-25-rework-catalog/design.md",
		"archive/2026-08-25-rework-catalog/proposal.md",
	} {
		if !exists(t, root, rel) {
			t.Fatalf("%s not archived", rel)
		}
	}
	if exists(t, root, "active/rework-catalog") {
		t.Fatal("source dir still present after archive")
	}
}

func TestArchiveChangeUnknownWork(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.ArchiveChange(t.Context(), mustWorkID(t), day)
	if err == nil {
		t.Fatal("ArchiveChange of unknown work = nil, want error")
	}
}

func TestLookupByIDReadsMetadataNotSuffix(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	writeProposal(t, root, workID, "rework-catalog", "rework-catalog")
	// Plant a decoy archive entry whose NAME would be the answer if lookup
	// trusted paths: same name, different date, different work id.
	decoy := artifact.NewDocument(artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: mustWorkID(t),
		Name: "rework-catalog", Kind: artifact.KindProposal,
	})
	writeDoc(t, root, "archive/2026-08-24-rework-catalog/proposal.md", decoy)
	if _, err := svc.ArchiveChange(t.Context(), workID, day); err != nil {
		t.Fatalf("ArchiveChange: %v", err)
	}

	entry, err := svc.LookupByID(t.Context(), workID)
	if err != nil {
		t.Fatalf("LookupByID: %v", err)
	}
	if entry.Path != "archive/2026-08-25-rework-catalog" {
		t.Fatalf("LookupByID returned %q, want the real entry, not a name match", entry.Path)
	}
}

// TestLookupByIDPrefersRecordOverProposal proves a closed change resolves
// through record.md when both documents are present.
func TestLookupByIDPrefersRecordOverProposal(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	rec := artifact.NewDocument(artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: workID,
		Name: "rework-catalog", Kind: artifact.KindRecord,
	})
	writeDoc(t, root, "archive/2026-08-25-rework-catalog/record.md", rec)
	prop := artifact.NewDocument(artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: workID,
		Name: "rework-catalog", Kind: artifact.KindProposal,
	})
	writeDoc(t, root, "archive/2026-08-25-rework-catalog/proposal.md", prop)

	entry, err := svc.LookupByID(t.Context(), workID)
	if err != nil {
		t.Fatalf("LookupByID: %v", err)
	}
	if entry.Kind != artifact.KindRecord {
		t.Fatalf("entry.Kind = %q, want the record's kind", entry.Kind)
	}
}

func TestLookupByIDSuffixedChange(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	writeProposal(t, root, workID, "first", "rework-catalog")
	if _, err := svc.ArchiveChange(t.Context(), workID, day); err != nil {
		t.Fatalf("ArchiveChange: %v", err)
	}
	// Second same-name work archived the same day lands on -2; lookup must
	// still find it by id.
	workID2 := mustWorkID(t)
	writeProposal(t, root, workID2, "second", "rework-catalog")
	e2, err := svc.ArchiveChange(t.Context(), workID2, day)
	if err != nil {
		t.Fatalf("second ArchiveChange: %v", err)
	}
	if e2.Path != "archive/2026-08-25-rework-catalog-2" {
		t.Fatalf("second entry path = %q, want -2 suffix", e2.Path)
	}
	entry, err := svc.LookupByID(t.Context(), workID2)
	if err != nil {
		t.Fatalf("LookupByID(suffixed): %v", err)
	}
	if entry.Path != "archive/2026-08-25-rework-catalog-2" {
		t.Fatalf("LookupByID = %q, want suffixed path", entry.Path)
	}
}

func TestLookupByIDTaskAndChange(t *testing.T) {
	svc, root := newService(t)
	taskWork := mustWorkID(t)
	changeWork := mustWorkID(t)
	writeTaskDoc(t, root, taskWork, "fix-login", "fix-login")
	writeProposal(t, root, changeWork, "rework-catalog", "rework-catalog")

	if _, err := svc.ArchiveTask(t.Context(), "active/fix-login/tasks.md", day); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	if _, err := svc.ArchiveChange(t.Context(), changeWork, day); err != nil {
		t.Fatalf("ArchiveChange: %v", err)
	}

	taskEntry, err := svc.LookupByID(t.Context(), taskWork)
	if err != nil {
		t.Fatalf("LookupByID(task): %v", err)
	}
	if taskEntry.IsDir || taskEntry.Path != "archive/2026-08-25-fix-login.md" {
		t.Fatalf("task entry = %+v", taskEntry)
	}

	changeEntry, err := svc.LookupByID(t.Context(), changeWork)
	if err != nil {
		t.Fatalf("LookupByID(change): %v", err)
	}
	if !changeEntry.IsDir || changeEntry.Path != "archive/2026-08-25-rework-catalog" {
		t.Fatalf("change entry = %+v", changeEntry)
	}
}

func TestLookupByIDUnknown(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.LookupByID(t.Context(), mustWorkID(t)); err == nil {
		t.Fatal("LookupByID of unknown work = nil, want error")
	}
}

func TestLookupByIDIgnoresNonArtifactEntries(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	writeTaskDoc(t, root, workID, "fix-login", "fix-login")
	// Plant junk that must not crash or match: a non-markdown file, a
	// markdown file with no metadata block, and a directory with no
	// artifact in it.
	writeFile(t, root, "archive/README.txt", []byte("hi"))
	writeFile(t, root, "archive/2026-08-01-notes.md", []byte("# not an artifact\n"))
	if err := os.MkdirAll(filepath.Join(root, "archive", "empty-dir"), 0o700); err != nil {
		t.Fatalf("mkdir empty-dir: %v", err)
	}
	if _, err := svc.ArchiveTask(t.Context(), "active/fix-login/tasks.md", day); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	entry, err := svc.LookupByID(t.Context(), workID)
	if err != nil {
		t.Fatalf("LookupByID: %v", err)
	}
	if entry.Path != "archive/2026-08-25-fix-login.md" {
		t.Fatalf("entry = %+v", entry)
	}
}

// TestArchiveWorkDispatchesTaskVersusChange proves the seam the artifact
// service calls: ArchiveWork finds the work by scanning active metadata
// and picks the file (task) or directory (change) move.
func TestArchiveWorkDispatchesTaskVersusChange(t *testing.T) {
	svc, root := newService(t)
	taskWork := mustWorkID(t)
	changeWork := mustWorkID(t)
	writeTaskDoc(t, root, taskWork, "fix-login", "fix-login")
	writeProposal(t, root, changeWork, "rework-catalog", "rework-catalog")

	te, err := svc.ArchiveWork(t.Context(), taskWork, day)
	if err != nil {
		t.Fatalf("ArchiveWork(task): %v", err)
	}
	ce, err := svc.ArchiveWork(t.Context(), changeWork, day)
	if err != nil {
		t.Fatalf("ArchiveWork(change): %v", err)
	}
	if te.IsDir {
		t.Fatal("task archived as a directory")
	}
	if !ce.IsDir {
		t.Fatal("change archived as a file")
	}
	if te.Name != "fix-login" || ce.Name != "rework-catalog" {
		t.Fatalf("names wrong: task=%q change=%q", te.Name, ce.Name)
	}
	if te.Date != "2026-08-25" || ce.Date != "2026-08-25" {
		t.Fatalf("dates wrong: task=%q change=%q", te.Date, ce.Date)
	}
	if _, err := svc.LookupByID(t.Context(), taskWork); err != nil {
		t.Fatalf("LookupByID(task): %v", err)
	}
	if _, err := svc.LookupByID(t.Context(), changeWork); err != nil {
		t.Fatalf("LookupByID(change): %v", err)
	}
}

func TestArchiveWorkUnknown(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.ArchiveWork(t.Context(), mustWorkID(t), day); err == nil {
		t.Fatal("ArchiveWork of unknown work = nil, want error")
	}
}

// TestArchiveRefusesSymlinkedSource proves the move stays inside securefs:
// a symlinked active document is refused, never followed.
func TestArchiveRefusesSymlinkedSource(t *testing.T) {
	svc, root := newService(t)
	workID := mustWorkID(t)
	writeTaskDoc(t, root, workID, "real", "fix-login")
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(outside, []byte("stolen\n"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	linkDir := filepath.Join(root, ActiveDir, "linked")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatalf("mkdir linked: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(linkDir, "tasks.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := svc.ArchiveTask(t.Context(), "active/linked/tasks.md", day); err == nil {
		t.Fatal("ArchiveTask through a symlink = nil error, want refusal")
	}
}

// TestSecurefsRootStillConfines is a guard on the assumption archive rests
// on: reads through the confined root cannot escape it.
func TestSecurefsRootStillConfines(t *testing.T) {
	_, root := newService(t)
	r, err := securefs.OpenRoot(root)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer r.Close()
	if _, err := r.ReadFile("../escape"); err == nil {
		t.Fatal("ReadFile(\"../escape\") = nil error, want refusal")
	}
}
