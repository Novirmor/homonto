package artifact

import (
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/workname"
)

// ErrNoFileName reports a kind that has no fixed file name inside a work
// directory — ADRs, whose path the repository's own convention decides.
var ErrNoFileName = errors.New("artifact: kind has no fixed file name")

// ActiveDir is the directory active work lives in, under the control
// repository root. Archived work moves out of it; see package archive.
const ActiveDir = "active"

// fileNames is the canonical file name of each kind inside a work
// directory. A task's single document and a change's tasks.md share the
// name tasks.md: they never occur in the same work, because a work is
// either a Task or a Change, and the kind in the metadata block — not the
// file name — is what identifies the document.
var fileNames = map[Kind]string{
	KindTaskDocument: "tasks.md",
	KindProposal:     "proposal.md",
	KindDesign:       "design.md",
	KindTasks:        "tasks.md",
	KindPresetTasks:  "tasks.preset.md",
	KindPlan:         "plan.md",
	KindFix:          "fix.md",
	KindTweak:        "tweak.md",
	KindVerification: "verification.md",
	KindRecord:       "record.md",
}

// FileName returns the canonical file name of a kind inside a work
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

// WorkDir returns the control-root-relative directory a work's documents
// live in while active.
func WorkDir(name string) (string, error) {
	if err := workname.Validate(name); err != nil {
		return "", err
	}
	return ActiveDir + "/" + name, nil
}

// Path returns the control-root-relative path of a work's document of one
// kind while the work is active.
func Path(name string, k Kind) (string, error) {
	dir, err := WorkDir(name)
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
