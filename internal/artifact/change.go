package artifact

import (
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/workname"
)

// ErrNoFileName reports a kind that has no fixed file name inside a work
// directory — ADRs, whose path the repository's own convention decides.
var ErrNoFileName = errors.New("artifact: kind has no fixed file name")

// The document layout inside the control repository. Records are ordinary
// repository documents under docs/, not hidden state: they are meant to be
// read, reviewed, and committed like anything else a project keeps.
//
//	docs/homonto/tasks/<name>.md                    one active task
//	docs/homonto/tasks/archive/<date>-<name>.md     an archived task
//	docs/homonto/changes/<name>/                    one active change
//	docs/homonto/changes/archive/<date>-<name>/     an archived change
//
// A task is ONE file and a change is a DIRECTORY, which is the whole
// difference in ceremony between them made visible on disk.
const (
	// DocsDir is the root of Homonto's documents in the control
	// repository.
	DocsDir = "docs/homonto"
	// TasksDir holds active task documents.
	TasksDir = DocsDir + "/tasks"
	// ChangesDir holds active change directories.
	ChangesDir = DocsDir + "/changes"
	// ArchiveName is the archive subdirectory of each.
	ArchiveName = "archive"
	// TasksArchiveDir holds archived task documents.
	TasksArchiveDir = TasksDir + "/" + ArchiveName
	// ChangesArchiveDir holds archived change directories.
	ChangesArchiveDir = ChangesDir + "/" + ArchiveName
)

// fileNames is the canonical file name of each kind inside a change
// directory. KindTaskDocument is absent on purpose: a task is a single
// file named after the work, not a document inside a directory.
var fileNames = map[Kind]string{
	KindProposal:     "proposal.md",
	KindDesign:       "design.md",
	KindTasks:        "tasks.md",
	KindPresetTasks:  "preset-tasks.md",
	KindPlan:         "plan.md",
	KindFix:          "fix.md",
	KindTweak:        "tweak.md",
	KindVerification: "verification.md",
	KindRecord:       "record.md",
}

// FileName returns the canonical file name of a kind inside a change
// directory.
func FileName(k Kind) (string, error) {
	name, ok := fileNames[k]
	if !ok {
		if k.known() {
			return "", fmt.Errorf("artifact: %s: %w", k, ErrNoFileName)
		}
		return "", fmt.Errorf("artifact: %q is not a known document kind", k)
	}
	return name, nil
}

// TaskPath returns the control-root-relative path of an active task's
// single document.
func TaskPath(name string) (string, error) {
	if err := workname.Validate(name); err != nil {
		return "", err
	}
	return TasksDir + "/" + name + ".md", nil
}

// ChangeDir returns the control-root-relative directory an active change's
// documents live in.
func ChangeDir(name string) (string, error) {
	if err := workname.Validate(name); err != nil {
		return "", err
	}
	return ChangesDir + "/" + name, nil
}

// Path returns the control-root-relative path of a work's document of one
// kind while the work is active.
func Path(name string, k Kind) (string, error) {
	if k == KindTaskDocument {
		return TaskPath(name)
	}
	dir, err := ChangeDir(name)
	if err != nil {
		return "", err
	}
	file, err := FileName(k)
	if err != nil {
		return "", err
	}
	return dir + "/" + file, nil
}

// hostKinds are the document kinds a host authors, in the phase the
// ownership table gives it. They are listed here so callers can scaffold
// a work's documents without duplicating the table.
var hostKinds = map[Phase][]Kind{
	PhasePlan:   {KindTaskDocument},
	PhaseOpen:   {KindProposal, KindFix, KindTweak, KindTasks},
	PhaseDesign: {KindDesign, KindTasks},
	PhaseBuild:  {KindPlan},
}

// HostKinds returns the document kinds a host authors in a phase.
func HostKinds(p Phase) []Kind {
	return append([]Kind(nil), hostKinds[p]...)
}
