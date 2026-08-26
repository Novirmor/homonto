package archive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/identity"
)

var archiveDay = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func archiveService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range Dirs() {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	svc, err := NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, root
}

func archiveWorkID(t *testing.T) identity.WorkID {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	return id
}

func writeArchiveDocument(t *testing.T, root, rel string, meta artifact.Metadata) {
	t.Helper()
	doc := artifact.NewDocument(meta)
	if meta.Kind == artifact.KindTaskDocument {
		doc.Regions = []artifact.RegionContent{
			{Region: artifact.RegionTaskGoal, Content: []byte("goal\n")},
			{Region: artifact.RegionTaskChecklist, Content: []byte("- [ ] item\n")},
			{Region: artifact.RegionTaskEvidence},
		}
	} else {
		doc.Regions = []artifact.RegionContent{{Region: artifact.RegionWholeDocument, Content: []byte("proposal\n")}}
	}
	body, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir document parent: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
}

func TestNewServiceRejectsInvalidRoot(t *testing.T) {
	for _, root := range []string{"", "relative"} {
		if _, err := NewService(root); err == nil {
			t.Errorf("NewService(%q) succeeded", root)
		}
	}
}

func TestArchiveWorkDispatchesByMetadataIdentity(t *testing.T) {
	svc, root := archiveService(t)
	taskID, changeID := archiveWorkID(t), archiveWorkID(t)
	writeArchiveDocument(t, root, TasksDir+"/unrelated-name.md", artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: taskID, Name: "fix-login", Kind: artifact.KindTaskDocument,
	})
	writeArchiveDocument(t, root, ChangesDir+"/other-name/proposal.md", artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: changeID, Name: "rework-catalog", Kind: artifact.KindProposal,
	})

	taskEntry, err := svc.ArchiveWork(t.Context(), taskID, archiveDay)
	if err != nil {
		t.Fatalf("ArchiveWork(task): %v", err)
	}
	changeEntry, err := svc.ArchiveWork(t.Context(), changeID, archiveDay)
	if err != nil {
		t.Fatalf("ArchiveWork(change): %v", err)
	}
	if taskEntry.IsDir || taskEntry.Path != TasksArchiveDir+"/2026-08-25-fix-login.md" || taskEntry.WorkID != taskID {
		t.Fatalf("task entry = %+v", taskEntry)
	}
	if !changeEntry.IsDir || changeEntry.Path != ChangesArchiveDir+"/2026-08-25-rework-catalog" || changeEntry.WorkID != changeID {
		t.Fatalf("change entry = %+v", changeEntry)
	}
}

func TestArchiveWorkRefusesUnknownAndSymlinkedWork(t *testing.T) {
	svc, root := archiveService(t)
	if _, err := svc.ArchiveWork(t.Context(), archiveWorkID(t), archiveDay); !errors.Is(err, ErrNotActive) {
		t.Fatalf("ArchiveWork(unknown) error = %v, want ErrNotActive", err)
	}

	id := archiveWorkID(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeArchiveDocument(t, filepath.Dir(outside), filepath.Base(outside), artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: id, Name: "fix-login", Kind: artifact.KindTaskDocument,
	})
	if err := os.Symlink(outside, filepath.Join(root, filepath.FromSlash(TasksDir), "linked.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// securefs opens the final component O_NOFOLLOW, so the symlinked
	// document is unreadable: the work resolves to nothing active and is
	// refused — never followed into the archive.
	if _, err := svc.ArchiveWork(t.Context(), id, archiveDay); !errors.Is(err, ErrNotActive) {
		t.Fatalf("ArchiveWork(symlinked) error = %v, want ErrNotActive", err)
	}
}
