package archive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/securefs"
)

// The directories this service moves work between. They are package
// artifact's spellings, shared so the two can never drift.
const (
	// TasksDir holds active task documents.
	TasksDir = artifact.TasksDir
	// TasksArchiveDir holds archived task documents.
	TasksArchiveDir = artifact.TasksArchiveDir
	// ChangesDir holds active change directories.
	ChangesDir = artifact.ChangesDir
	// ChangesArchiveDir holds archived change directories.
	ChangesArchiveDir = artifact.ChangesArchiveDir
)

// Dirs returns every directory the service reads or writes. Callers
// scaffold them; the archive service never creates a directory itself.
func Dirs() []string {
	return []string{TasksDir, TasksArchiveDir, ChangesDir, ChangesArchiveDir}
}

// ErrNotFound reports a lookup that found no archived work with the given
// id.
var ErrNotFound = errors.New("archive: no archived work with that id")

// ErrNotActive reports an archive operation whose work id matches no
// active work.
var ErrNotActive = errors.New("archive: no active work with that id")

// Entry describes one archived work: where it landed and the identity its
// metadata declares.
type Entry struct {
	WorkID identity.WorkID
	Name   string // normalized work name (no date, no suffix)
	Date   string // YYYY-MM-DD
	Kind   artifact.Kind
	Path   string // control-root-relative path (file or directory)
	IsDir  bool
}

// Service moves finished work into the archive and resolves archived
// entries by work id. It holds the absolute control repository root and
// opens a confined securefs root per operation, the same way registration
// and lease recovery do; the archive directory and its parents must
// already exist.
type Service struct {
	root string
}

// NewService binds an archive service to the absolute control repository
// root.
func NewService(root string) (*Service, error) {
	if root == "" {
		return nil, fmt.Errorf("archive: root must not be empty")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("archive: root %q must be an absolute path", root)
	}
	return &Service{root: root}, nil
}

// Root returns the absolute control repository root the service is bound
// to (for callers that need os-level operations beside it, e.g. mkdir).
func (s *Service) Root() string { return s.root }

// open opens a fresh confined root on the control repository. Callers
// close it; one root serves one public operation so no descriptor
// outlives the call that needed it.
func (s *Service) open() (*securefs.Root, error) {
	return securefs.OpenRoot(s.root)
}

// ArchiveTask moves the task document at srcRel (control-root-relative,
// docs/homonto/tasks/<name>.md) into the task archive. The work's name and
// identity come from the document's metadata block, never the path.
func (s *Service) ArchiveTask(ctx context.Context, srcRel string, date time.Time) (Entry, error) {
	root, err := s.open()
	if err != nil {
		return Entry{}, fmt.Errorf("archive: open %s: %w", s.root, err)
	}
	defer root.Close()
	return s.archiveTask(ctx, root, srcRel, date)
}

// archiveTask is ArchiveTask on an already-open root.
func (s *Service) archiveTask(ctx context.Context, root *securefs.Root, srcRel string, date time.Time) (Entry, error) {
	meta, err := readMetadata(root, srcRel)
	if err != nil {
		return Entry{}, err
	}
	if meta.Kind != artifact.KindTaskDocument {
		return Entry{}, fmt.Errorf("archive: %s is a %s document, not a task", srcRel, meta.Kind)
	}
	base, err := Name(date, meta.Name, func(c string) bool {
		return s.exists(TasksArchiveDir + "/" + c + ".md")
	})
	if err != nil {
		return Entry{}, err
	}
	dest := TasksArchiveDir + "/" + base + ".md"
	if err := root.Rename(srcRel, dest); err != nil {
		return Entry{}, fmt.Errorf("archive: move %s to %s: %w", srcRel, dest, err)
	}
	return Entry{
		WorkID: meta.WorkID,
		Name:   meta.Name,
		Date:   date.Format(dateFormat),
		Kind:   meta.Kind,
		Path:   dest,
	}, nil
}

// ArchiveChange moves the active change whose metadata carries workID into
// the change archive. The active directory is found by scanning the
// changes directory and reading each identity document — identity comes
// from the document, never the directory name.
func (s *Service) ArchiveChange(ctx context.Context, workID identity.WorkID, date time.Time) (Entry, error) {
	root, err := s.open()
	if err != nil {
		return Entry{}, fmt.Errorf("archive: open %s: %w", s.root, err)
	}
	defer root.Close()
	return s.archiveChange(ctx, root, workID, date)
}

// archiveChange is ArchiveChange on an already-open root.
func (s *Service) archiveChange(ctx context.Context, root *securefs.Root, workID identity.WorkID, date time.Time) (Entry, error) {
	dir, meta, err := s.findActive(root, workID)
	if err != nil {
		return Entry{}, err
	}
	base, err := Name(date, meta.Name, func(c string) bool {
		return s.exists(ChangesArchiveDir + "/" + c)
	})
	if err != nil {
		return Entry{}, err
	}
	dest := ChangesArchiveDir + "/" + base
	if err := root.Rename(dir, dest); err != nil {
		return Entry{}, fmt.Errorf("archive: move %s to %s: %w", dir, dest, err)
	}
	return Entry{
		WorkID: meta.WorkID,
		Name:   meta.Name,
		Date:   date.Format(dateFormat),
		Kind:   meta.Kind,
		Path:   dest,
		IsDir:  true,
	}, nil
}

// ArchiveWork is the seam the artifact service calls: it resolves workID
// against the active tree and archives the task file or the change
// directory under date. ArchiveWork never guesses from names — a work with
// no matching active artifact is ErrNotActive.
func (s *Service) ArchiveWork(ctx context.Context, workID identity.WorkID, date time.Time) (Entry, error) {
	root, err := s.open()
	if err != nil {
		return Entry{}, fmt.Errorf("archive: open %s: %w", s.root, err)
	}
	defer root.Close()
	dir, meta, err := s.findActive(root, workID)
	if err != nil {
		return Entry{}, err
	}
	if meta.Kind == artifact.KindTaskDocument {
		return s.archiveTask(ctx, root, dir, date)
	}
	return s.archiveChange(ctx, root, workID, date)
}

// LookupByID resolves the archived entry whose metadata carries workID,
// searching both archives — a task file and a change directory are
// different shapes, and the caller should not have to know which it is
// asking about. Every candidate is read for its metadata: the name suffix
// and the directory name are never trusted, and non-artifact entries
// (loose files, empty directories) are skipped. A missing, unreadable, or
// corrupt candidate is skipped too, so a torn archive never blocks lookups
// of the rest.
func (s *Service) LookupByID(ctx context.Context, workID identity.WorkID) (Entry, error) {
	root, err := s.open()
	if err != nil {
		return Entry{}, fmt.Errorf("archive: open %s: %w", s.root, err)
	}
	defer root.Close()

	if entry, found, err := s.lookupTask(root, workID); err != nil {
		return Entry{}, err
	} else if found {
		return entry, nil
	}
	if entry, found, err := s.lookupChange(root, workID); err != nil {
		return Entry{}, err
	} else if found {
		return entry, nil
	}
	return Entry{}, fmt.Errorf("archive: work %s: %w", workID, ErrNotFound)
}

// lookupTask searches the archived task documents.
func (s *Service) lookupTask(root *securefs.Root, workID identity.WorkID) (Entry, bool, error) {
	entries, err := s.readDir(TasksArchiveDir)
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		rel := TasksArchiveDir + "/" + e.Name()
		meta, err := readMetadata(root, rel)
		if err != nil || meta.WorkID != workID {
			continue
		}
		return Entry{
			WorkID: meta.WorkID, Name: meta.Name, Date: dateOf(e.Name()),
			Kind: meta.Kind, Path: rel,
		}, true, nil
	}
	return Entry{}, false, nil
}

// lookupChange searches the archived change directories. A closed change
// is identified by its record when it has one and by its proposal
// otherwise, because an upgraded preset has both and the record is the
// later, more authoritative document.
func (s *Service) lookupChange(root *securefs.Root, workID identity.WorkID) (Entry, bool, error) {
	entries, err := s.readDir(ChangesArchiveDir)
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := ChangesArchiveDir + "/" + e.Name()
		for _, doc := range changeIdentityDocs {
			meta, err := readMetadata(root, rel+"/"+doc)
			if err != nil || meta.WorkID != workID {
				continue
			}
			return Entry{
				WorkID: meta.WorkID, Name: meta.Name, Date: dateOf(e.Name()),
				Kind: meta.Kind, Path: rel, IsDir: true,
			}, true, nil
		}
	}
	return Entry{}, false, nil
}

// changeIdentityDocs are the documents a change's identity is read from,
// in order of authority.
var changeIdentityDocs = []string{"record.md", "proposal.md", "fix.md", "tweak.md"}

// readDir lists a directory under the control root, treating a missing
// directory as empty: a workspace that has archived no change yet has no
// change archive, and that is not a failure.
func (s *Service) readDir(rel string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(s.absPath(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("archive: list %s: %w", rel, err)
	}
	return entries, nil
}

// findActive locates the active work whose metadata matches workID: a task
// is one file under the tasks directory, a change is a directory under the
// changes directory. Names are ignored for identity.
func (s *Service) findActive(root *securefs.Root, workID identity.WorkID) (string, artifact.Metadata, error) {
	files, err := s.readDir(TasksDir)
	if err != nil {
		return "", artifact.Metadata{}, err
	}
	for _, e := range files {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		rel := TasksDir + "/" + e.Name()
		meta, err := readMetadata(root, rel)
		if err != nil || meta.WorkID != workID {
			continue
		}
		return rel, meta, nil
	}
	dirs, err := s.readDir(ChangesDir)
	if err != nil {
		return "", artifact.Metadata{}, err
	}
	for _, e := range dirs {
		if !e.IsDir() || e.Name() == artifact.ArchiveName {
			continue
		}
		dir := ChangesDir + "/" + e.Name()
		for _, doc := range changeIdentityDocs {
			meta, err := readMetadata(root, dir+"/"+doc)
			if err != nil || meta.WorkID != workID {
				continue
			}
			return dir, meta, nil
		}
	}
	return "", artifact.Metadata{}, fmt.Errorf("archive: work %s: %w", workID, ErrNotActive)
}

// readMetadata reads and parses the artifact metadata block of the file at
// rel. A missing file or an unparsable document is an error (callers
// decide whether that skips a candidate).
func readMetadata(root *securefs.Root, rel string) (artifact.Metadata, error) {
	b, err := root.ReadFile(rel)
	if err != nil {
		return artifact.Metadata{}, fmt.Errorf("archive: read %s: %w", rel, err)
	}
	meta, err := artifact.ParseMetadata(b)
	if err != nil {
		return artifact.Metadata{}, fmt.Errorf("archive: parse %s: %w", rel, err)
	}
	return meta, nil
}

// exists reports whether anything occupies rel under the control root. It
// is advisory: it only picks a free archive name, and the move that
// follows goes through securefs, which refuses symlinked components.
func (s *Service) exists(rel string) bool {
	_, err := os.Lstat(s.absPath(rel))
	return err == nil
}

// absPath joins a control-root-relative rel onto the control root.
func (s *Service) absPath(rel string) string {
	return filepath.Join(s.root, filepath.FromSlash(rel))
}
