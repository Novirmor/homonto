package artifact

import (
	"errors"
	"testing"
)

func TestFileNameCoversEveryPersistedKind(t *testing.T) {
	want := map[Kind]string{
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
	for kind, file := range want {
		got, err := FileName(kind)
		if err != nil {
			t.Errorf("FileName(%s): %v", kind, err)
			continue
		}
		if got != file {
			t.Errorf("FileName(%s) = %q, want %q", kind, got, file)
		}
	}
	// A task is one file named after the work, not a document inside a
	// directory, and an ADR is a plain repository document with no fixed
	// slot. Neither has a file name inside a change directory.
	for _, kind := range []Kind{KindTaskDocument, KindADR} {
		if _, err := FileName(kind); !errors.Is(err, ErrNoFileName) {
			t.Errorf("FileName(%s) error = %v, want ErrNoFileName", kind, err)
		}
	}
	if _, err := FileName(Kind("invoice")); err == nil {
		t.Error("FileName(unknown kind) = nil error, want rejection")
	}
}

// TestFileNameKnowsEveryKind guards against a kind added to kind.go and
// forgotten here.
func TestFileNameKnowsEveryKind(t *testing.T) {
	all := []Kind{
		KindTaskDocument, KindProposal, KindDesign, KindTasks, KindPresetTasks,
		KindPlan, KindFix, KindTweak, KindVerification, KindRecord, KindADR,
	}
	for _, k := range all {
		if !k.known() {
			t.Errorf("kind %q is not known()", k)
		}
		_, err := FileName(k)
		if err != nil && !errors.Is(err, ErrNoFileName) {
			t.Errorf("FileName(%s) = %v, want a name or ErrNoFileName", k, err)
		}
	}
}

// TestPathsFollowTheDocumentLayout pins where records live: a task is one
// file, a change is a directory, and both sit under docs/ where a reviewer
// will find them rather than in hidden state.
func TestPathsFollowTheDocumentLayout(t *testing.T) {
	dir, err := ChangeDir("rework-catalog")
	if err != nil {
		t.Fatalf("ChangeDir: %v", err)
	}
	if dir != "docs/homonto/changes/rework-catalog" {
		t.Fatalf("ChangeDir = %q", dir)
	}
	tests := map[Kind]string{
		KindTaskDocument: "docs/homonto/tasks/fix-login.md",
		KindProposal:     "docs/homonto/changes/fix-login/proposal.md",
		KindTasks:        "docs/homonto/changes/fix-login/tasks.md",
		KindPresetTasks:  "docs/homonto/changes/fix-login/preset-tasks.md",
		KindRecord:       "docs/homonto/changes/fix-login/record.md",
	}
	for kind, want := range tests {
		got, err := Path("fix-login", kind)
		if err != nil {
			t.Errorf("Path(%s): %v", kind, err)
			continue
		}
		if got != want {
			t.Errorf("Path(%s) = %q, want %q", kind, got, want)
		}
	}
	// A task's document is not inside a directory named after it, so a
	// task and a change of the same name never collide.
	task, _ := Path("x", KindTaskDocument)
	change, _ := Path("x", KindTasks)
	if task == change {
		t.Fatalf("a task and a change document share the path %q", task)
	}
}

func TestPathValidatesTheWorkName(t *testing.T) {
	for _, name := range []string{"", "Fix Login", "archive", "active", "-leading", "trailing-"} {
		if _, err := ChangeDir(name); err == nil {
			t.Errorf("ChangeDir(%q) = nil error, want workname rejection", name)
		}
		if _, err := TaskPath(name); err == nil {
			t.Errorf("TaskPath(%q) = nil error, want workname rejection", name)
		}
		if _, err := Path(name, KindProposal); err == nil {
			t.Errorf("Path(%q) = nil error, want workname rejection", name)
		}
	}
}
