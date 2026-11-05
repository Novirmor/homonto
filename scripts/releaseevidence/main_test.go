package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/starter"
)

func fakeGit(t *testing.T, commit, dirty string) {
	t.Helper()
	prev := git
	git = func(args ...string) (string, error) {
		if args[0] == "rev-parse" {
			return commit, nil
		}
		return dirty, nil
	}
	t.Cleanup(func() { git = prev })
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// Every starter cell's evidence is required, and the manifest blocks the tag
// until each key passed at the release commit.
func TestManifestBlocksUntilEveryKeyPassed(t *testing.T) {
	fakeGit(t, "abc123", "")
	path := filepath.Join(t.TempDir(), "evidence.json")
	runOK(t, "init", "--version", "v1.0.0", "--out", path)
	var out bytes.Buffer
	if err := run([]string{"check", path}, &out); err == nil || !strings.Contains(err.Error(), "cannot be tagged") {
		t.Fatalf("an empty manifest allowed the tag: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range Requirements() {
		ids[r.ID] = true
	}
	for _, c := range starter.Cells() {
		for _, e := range starter.CellEvidence(c[0], c[1]) {
			if !ids[e.ID] {
				t.Errorf("cell %v evidence %s is not required", c, e.ID)
			}
		}
	}
	for _, r := range Requirements() {
		if r.ID == "compat/upgrade" {
			runOK(t, "record", path, r.ID, "n/a", "--note", "first 1.x release")
			continue
		}
		runOK(t, "record", path, r.ID, "pass", "--note", "ran")
	}
	if got := runOK(t, "check", path); !strings.Contains(got, "may be tagged at abc123") || strings.Count(got, "claimable:") != len(starter.Cells()) {
		t.Fatalf("check output:\n%s", got)
	}
}

func TestSkipAndMissingStayVisible(t *testing.T) {
	fakeGit(t, "abc123", "")
	path := filepath.Join(t.TempDir(), "evidence.json")
	runOK(t, "init", "--version", "v1.0.0", "--out", path)
	runOK(t, "record", path, "sandbox/gitlab", "skip", "--note", "no sandbox.env")
	var out bytes.Buffer
	err := run([]string{"check", path}, &out)
	if err == nil || !strings.Contains(out.String(), "skip     sandbox/gitlab") || !strings.Contains(out.String(), "missing  gate/full") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "claimable: host gitlab") {
		t.Error("a cell with a skipped key was claimable")
	}
}

func TestRecordRefusesOtherCommitsAndBadValues(t *testing.T) {
	fakeGit(t, "abc123", "")
	path := filepath.Join(t.TempDir(), "evidence.json")
	runOK(t, "init", "--version", "v1.0.0", "--out", path)
	for name, tc := range map[string]struct {
		commit, dirty string
		args          []string
		want          string
	}{
		"other commit": {"def456", "", []string{"record", path, "gate/full", "pass"}, "evidence from another commit does not count"},
		"dirty tree":   {"abc123", " M x.go", []string{"record", path, "gate/full", "pass"}, "uncommitted changes"},
		"unknown key":  {"abc123", "", []string{"record", path, "gate/nope", "pass"}, "is not a key"},
		"bad status":   {"abc123", "", []string{"record", path, "gate/full", "green"}, "is not pass, fail, skip or n/a"},
		"cell n/a":     {"abc123", "", []string{"record", path, "sandbox/github", "n/a", "--note", "x"}, "cannot be n/a"},
		"n/a no note":  {"abc123", "", []string{"record", path, "compat/upgrade", "n/a"}, "needs a --note"},
	} {
		t.Run(name, func(t *testing.T) {
			fakeGit(t, tc.commit, tc.dirty)
			var out bytes.Buffer
			if err := run(tc.args, &out); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	fakeGit(t, "abc123", "")
	var out bytes.Buffer
	if err := run([]string{"init", "--version", "v1.0.0", "--out", path}, &out); err == nil {
		t.Error("init reset an existing manifest")
	}
}

func TestCheckRefusesEvidenceForAnotherCommit(t *testing.T) {
	fakeGit(t, "abc123", "")
	path := filepath.Join(t.TempDir(), "evidence.json")
	runOK(t, "init", "--version", "v1.0.0", "--out", path)

	fakeGit(t, "def456", "")
	var out bytes.Buffer
	err := run([]string{"check", path}, &out)
	if err == nil || !strings.Contains(err.Error(), "evidence from another commit does not allow this tag") {
		t.Fatalf("check accepted another commit's evidence: %v\n%s", err, out.String())
	}
}
