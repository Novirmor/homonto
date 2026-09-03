package integrationrecord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureEntry(alias string) Entry {
	return Entry{
		Alias: alias, BaseBranch: "main", BaseCommit: "1111111111111111111111111111111111111111",
		SourceBranch: "change/c", SourceCommit: "2222222222222222222222222222222222222222",
	}
}

func TestCompleteForValidatesModeSpecificReceipt(t *testing.T) {
	for _, tc := range []struct {
		mode, receipt string
		valid         bool
	}{
		{mode: "merge", receipt: "merge:abcdef1", valid: true},
		{mode: "merge", receipt: "pr:https://example.com/1"},
		{mode: "pr", receipt: "pr:https://example.com/pull/1", valid: true},
		{mode: "pr", receipt: "pr:http://example.com/1"},
	} {
		record := NewPending("c", tc.mode, "main", []Entry{fixtureEntry("")})
		completed, err := record.CompleteFor("", tc.receipt)
		if tc.valid && (err != nil || completed.Status != StatusComplete) {
			t.Errorf("CompleteFor(%s, %s) = %+v, %v", tc.mode, tc.receipt, completed, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("CompleteFor(%s, %s) succeeded", tc.mode, tc.receipt)
		}
	}
}

func TestCompleteForIsOneWayAndIdempotent(t *testing.T) {
	record := NewPending("c", "merge", "main", []Entry{fixtureEntry("")})
	completed, err := record.CompleteFor("", "merge:abcdef1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := completed.CompleteFor("", "merge:abcdef1"); err != nil {
		t.Fatalf("same receipt replay: %v", err)
	}
	if _, err := completed.CompleteFor("", "merge:1234567"); err == nil {
		t.Fatal("replacement receipt succeeded")
	}
	if _, err := completed.CompleteFor("api", "merge:abcdef1"); err == nil {
		t.Fatal("unknown repository entry succeeded")
	}
}

func TestCompleteForAggregateStatusNeedsEveryRepository(t *testing.T) {
	record := NewPending("c", "merge", "main", []Entry{fixtureEntry(""), fixtureEntry("api")})
	partial, err := record.CompleteFor("", "merge:abcdef1")
	if err != nil {
		t.Fatal(err)
	}
	if partial.Status != StatusPending {
		t.Fatalf("partial completion status = %q, want pending", partial.Status)
	}
	complete, err := partial.CompleteFor("api", "merge:abcdef2")
	if err != nil {
		t.Fatal(err)
	}
	if complete.Status != StatusComplete {
		t.Fatalf("full completion status = %q, want complete", complete.Status)
	}
	if err := complete.Validate("c"); err != nil {
		t.Fatalf("complete record invalid: %v", err)
	}
}

func TestValidateRejectsEntryBranchMismatch(t *testing.T) {
	entry := fixtureEntry("")
	entry.BaseBranch = "release"
	record := NewPending("c", "merge", "main", []Entry{entry})
	if err := record.Validate("c"); err == nil || !strings.Contains(err.Error(), "base branch") {
		t.Fatalf("entry/record branch mismatch accepted: %v", err)
	}
}

func TestSaveRefusesSymlinkedSidecarParent(t *testing.T) {
	changeDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(changeDir, ".onto")); err != nil {
		t.Fatal(err)
	}
	err := Save(changeDir, NewPending("c", "merge", "main", []Entry{fixtureEntry("")}))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Save accepted symlinked parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "integration.json")); !os.IsNotExist(err) {
		t.Fatalf("Save escaped the change directory: %v", err)
	}
}
