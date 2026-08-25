package artifact

import (
	"errors"
	"testing"
)

func TestFileNameCoversEveryPersistedKind(t *testing.T) {
	want := map[Kind]string{
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
	// ADRs are plain repository documents with no fixed slot.
	if _, err := FileName(KindADR); !errors.Is(err, ErrNoFileName) {
		t.Errorf("FileName(adr) error = %v, want ErrNoFileName", err)
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

func TestPathAndWorkDir(t *testing.T) {
	dir, err := WorkDir("fix-login")
	if err != nil {
		t.Fatalf("WorkDir: %v", err)
	}
	if dir != "active/fix-login" {
		t.Fatalf("WorkDir = %q, want active/fix-login", dir)
	}
	got, err := Path("fix-login", KindTaskDocument)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != "active/fix-login/tasks.md" {
		t.Fatalf("Path = %q, want active/fix-login/tasks.md", got)
	}
}

func TestPathValidatesTheWorkName(t *testing.T) {
	for _, name := range []string{"", "Fix Login", "archive", "active", "-leading", "trailing-"} {
		if _, err := WorkDir(name); err == nil {
			t.Errorf("WorkDir(%q) = nil error, want workname rejection", name)
		}
		if _, err := Path(name, KindProposal); err == nil {
			t.Errorf("Path(%q) = nil error, want workname rejection", name)
		}
	}
}

// TestHostKindsMatchTheOwnershipTable proves the scaffolding list and the
// ownership table cannot drift: every kind a phase lists must actually be
// host-writable in that phase.
func TestHostKindsMatchTheOwnershipTable(t *testing.T) {
	for _, phase := range []Phase{PhaseOpen, PhaseDesign, PhaseBuild, PhaseVerify, PhaseClose} {
		for _, kind := range HostKinds(phase) {
			owner, _, ok := Ownership(kind, phase)
			if !ok || owner != OwnerHost {
				t.Errorf("HostKinds(%s) lists %s, which the table gives to %q (found=%v)", phase, kind, owner, ok)
			}
		}
	}
	// And every host-owned pair in the table is listed.
	for kind, byPhase := range ownershipTable {
		for phase, row := range byPhase {
			if row.Owner != OwnerHost {
				continue
			}
			found := false
			for _, k := range HostKinds(phase) {
				if k == kind {
					found = true
				}
			}
			if !found {
				t.Errorf("ownership table gives the host %s in %s, but HostKinds(%s) omits it", kind, phase, phase)
			}
		}
	}
}
