package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/bypasslog"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestBypassCommandSkipsArtifactsAndRecordsAudit(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "recover", "open")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"bypass", "recover", "--to", "close", "--reason", "urgent recovery", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "recover", "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != "close" {
		t.Fatalf("phase = %q, want close", st.Phase)
	}
	sc, exists, err := bypasslog.Load(bypasslog.Path(filepath.Join(dir, "docs", "changes", "recover"), "onto"), "recover", "onto")
	if err != nil || !exists {
		t.Fatalf("audit = (%+v, %t, %v), want present", sc, exists, err)
	}
	got := sc.Records[0]
	if got.From != "open" || got.To != "close" || got.Reason != "urgent recovery" || got.Command == "" || got.At == "" || len(got.Skipped) == 0 {
		t.Fatalf("audit record = %+v, want complete explicit bypass record", got)
	}
}

func TestBypassCommandArchivesWithoutCloseEvidence(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "carry", "open", "unmerged-delta.md")

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"bypass", "carry", "--to", "archive", "--reason", "superseded", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	archived, err := filepath.Glob(filepath.Join(dir, "docs", "changes", "archive", "*-carry"))
	if err != nil || len(archived) != 1 {
		t.Fatalf("archive directories = %v, %v; want one", archived, err)
	}
	if _, err := os.Stat(filepath.Join(archived[0], "unmerged-delta.md")); err != nil {
		t.Fatal(err)
	}
	st, err := ontostate.Load(filepath.Join(archived[0], "onto-state.yaml"))
	if err != nil || !st.Archived {
		t.Fatalf("archived state = %+v, %v; want archived", st, err)
	}
	if _, exists, err := bypasslog.Load(bypasslog.Path(archived[0], "onto"), "carry", "onto"); err != nil || !exists {
		t.Fatalf("archived audit = (%t, %v), want present", exists, err)
	}
}

func TestBypassCommandRefusesSymlinkedArchiveParent(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "confined", "open")
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "docs", "changes", "archive")); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"bypass", "confined", "--to", "archive", "--reason", "test", "--dir", dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("bypass archive accepted a symlinked archive parent")
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "confined")); err != nil {
		t.Fatalf("change moved despite archive refusal: %v", err)
	}
}

func TestBypassCommandRefusesConcurrentBypassLock(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "locked", "open")
	lock := filepath.Join(dir, "docs", "changes", ".onto-bypass.lock")
	if err := os.WriteFile(lock, []byte("pid=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"bypass", "locked", "--to", "design", "--reason", "test", "--dir", dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("bypass accepted an existing lock")
	}
}
