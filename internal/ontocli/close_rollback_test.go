package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// TestCloseCommand_ArchivePreparationFailureLeavesStateUntouched proves that a
// failure before the move leaves the active workspace and flag unchanged.
func TestCloseCommand_ArchivePreparationFailureLeavesStateUntouched(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	// Force the archive move to fail deterministically: make the archive parent
	// a regular file so os.MkdirAll(docs/changes/archive) fails during close.
	// Committing it keeps the worktree clean so the dirty-worktree gate passes.
	archiveParent := filepath.Join(dir, "docs", "changes", "archive")
	if err := os.WriteFile(archiveParent, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "seed change + archive blocker")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error (archive move must fail)")
	}

	// The change directory must remain at its original path.
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); err != nil {
		t.Errorf("change dir should remain in place: %v", err)
	}
	// The archived flag was never written.
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Archived {
		t.Errorf("st.Archived = true after archive preparation failed, want false")
	}
}

func TestCloseCommand_StateSaveFailureRollsMoveBack(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	checkoutChangeBranch(t, dir, "demo")
	commitAll(t, dir, "seed change")
	changeDir := filepath.Join(dir, "docs", "changes", "demo")
	if err := os.Chmod(changeDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(changeDir, 0o755)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("close succeeded despite an unwritable archive workspace")
	}

	if err := os.Chmod(changeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("rolled-back state is not readable: %v", err)
	}
	if st.Archived {
		t.Fatal("rolled-back state records archived=true")
	}
	archiveDir := filepath.Join(dir, "docs", "changes", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed close left archive entries: %v", entries)
	}
}
