package tocli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/bypasslog"
	"github.com/noviopenworks/homonto/internal/tostate"
)

func TestBypassCommandCanReturnToPlanAndRecordsAudit(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "recover", "--dir", dir)
	run(t, false, "phase", "recover", "--dir", dir)
	run(t, false, "bypass", "recover", "--to", "plan", "--reason", "redo plan", "--dir", dir)

	st, err := tostate.Load(statePath(dir, "recover"))
	if err != nil || st.Phase != tostate.PhasePlan {
		t.Fatalf("state = %+v, %v; want plan", st, err)
	}
	sc, exists, err := bypasslog.Load(bypasslog.Path(changeDir(dir, "recover"), "to"), "recover", "to")
	if err != nil || !exists || len(sc.Records) != 1 {
		t.Fatalf("audit = (%+v, %t, %v), want one record", sc, exists, err)
	}
	if got := sc.Records[0]; got.From != tostate.PhaseDo || got.To != tostate.PhasePlan || got.Reason != "redo plan" {
		t.Fatalf("audit record = %+v", got)
	}
}

func TestBypassCommandArchivesUnverifiedChange(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "unfinished", "--dir", dir)
	run(t, false, "bypass", "unfinished", "--to", "done", "--reason", "release blocked", "--dir", dir)

	archived := findArchived(dir, "unfinished")
	st, err := tostate.Load(filepath.Join(archived, tostate.FileName))
	if err != nil || st.Phase != tostate.PhaseDone || st.Verified {
		t.Fatalf("archived state = %+v, %v; want unverified done state", st, err)
	}
	sc, exists, err := bypasslog.Load(bypasslog.Path(archived, "to"), "unfinished", "to")
	if err != nil || !exists || sc.Records[0].To != tostate.PhaseDone {
		t.Fatalf("audit = (%+v, %t, %v), want done record", sc, exists, err)
	}
}

func TestBypassCommandClearsVerifiedOnTerminalRecovery(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "wedged", "--dir", dir)
	if err := tostate.Save(statePath(dir, "wedged"), tostate.State{Change: "wedged", Phase: tostate.PhaseDone, Verified: true, Evidence: "old assertion", Finished: "2026-09-03"}); err != nil {
		t.Fatal(err)
	}
	run(t, false, "bypass", "wedged", "--to", "archive", "--reason", "verification unavailable", "--dir", dir)
	st, err := tostate.Load(filepath.Join(findArchived(dir, "wedged"), tostate.FileName))
	if err != nil || st.Verified || st.Evidence != "" {
		t.Fatalf("archived state = %+v, %v; want bypass-cleared verification", st, err)
	}
}

func TestBypassCommandRefusesSymlinkedArchiveParent(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "confined", "--dir", dir)
	if err := os.Symlink(t.TempDir(), archiveDir(dir)); err != nil {
		t.Fatal(err)
	}
	run(t, true, "bypass", "confined", "--to", "archive", "--reason", "test", "--dir", dir)
	if _, err := os.Stat(changeDir(dir, "confined")); err != nil {
		t.Fatalf("change moved despite archive refusal: %v", err)
	}
}
