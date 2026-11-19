package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/integrationrecord"
)

func TestSquashPRReceipt(t *testing.T) {
	l := explicitWorkspace(t)
	dir := l.Repos["a"]
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/repo.git")
	base, _ := resolveCommit(dir, "HEAD")
	runGit(t, dir, "checkout", "-b", "feature")
	writeFile(t, filepath.Join(dir, "feature"), "verified\n")
	commitAll(t, dir, "feature")
	head, _ := resolveCommit(dir, "HEAD")
	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "merge", "--squash", "feature")
	commitAll(t, dir, "squash")
	squash, _ := resolveCommit(dir, "HEAD")
	entry := integrationrecord.Entry{BaseBranch: "main", BaseCommit: base, SourceCommit: head}
	url := "https://github.com/acme/repo/pull/1"
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PR_RESPONSE", filepath.Join(bin, "response"))
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\ncat \"$PR_RESPONSE\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	response := func(h, merge, branch, state, repo string, merged bool) {
		t.Helper()
		data := fmt.Sprintf(`{"html_url":%q,"number":1,"state":%q,"merged":%t,"merge_commit_sha":%q,"head":{"sha":%q},"base":{"ref":%q,"repo":{"full_name":%q}}}`, url, state, merged, merge, h, branch, repo)
		if err := os.WriteFile(os.Getenv("PR_RESPONSE"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	receipt := "squash-pr:" + url
	response(head, squash, "main", "closed", "acme/repo", true)
	got, err := validateSquashPR(l.ConfigRoot, dir, receipt, entry)
	if err != nil || got != receipt+"#"+squash {
		t.Fatalf("valid receipt: %s %v", got, err)
	}
	if err := os.WriteFile(os.Getenv("PR_RESPONSE"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSquashPR(l.ConfigRoot, dir, receipt, entry); err == nil {
		t.Fatal("accepted malformed host response")
	}
	for _, tc := range []struct {
		name, head, merge, branch, state, repo string
		merged                                 bool
	}{
		{"wrong head", base, squash, "main", "closed", "acme/repo", true},
		{"wrong merge", head, base, "main", "closed", "acme/repo", true},
		{"wrong branch", head, squash, "other", "closed", "acme/repo", true},
		{"wrong repository", head, squash, "main", "closed", "acme/other", true},
		{"not merged", head, squash, "main", "closed", "acme/repo", false},
		{"open", head, squash, "main", "open", "acme/repo", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response(tc.head, tc.merge, tc.branch, tc.state, tc.repo, tc.merged)
			if _, err := validateSquashPR(l.ConfigRoot, dir, receipt, entry); err == nil {
				t.Fatal("accepted invalid PR")
			}
		})
	}
	response(head, squash, "main", "closed", "acme/repo", true)
	if _, err := validateSquashPR(l.ConfigRoot, dir, receipt+"#"+base, entry); err == nil {
		t.Fatal("accepted changed merge commit")
	}
	if _, err := validateSquashPR(l.ConfigRoot, dir, strings.Replace(receipt, "acme/repo", "acme/other", 1), entry); err == nil {
		t.Fatal("accepted wrong origin")
	}
	writeFile(t, filepath.Join(dir, "extra"), "unverified\n")
	commitAll(t, dir, "different tree")
	different, _ := resolveCommit(dir, "HEAD")
	response(head, different, "main", "closed", "acme/repo", true)
	if _, err := validateSquashPR(l.ConfigRoot, dir, receipt, entry); err == nil {
		t.Fatal("accepted different squash tree")
	}
	response(head, squash, "main", "closed", "acme/repo", true)
	runGit(t, dir, "update-ref", "refs/heads/main", base)
	if _, err := validateSquashPR(l.ConfigRoot, dir, receipt, entry); err == nil {
		t.Fatal("accepted squash outside base branch")
	}
}

func TestSquashPRCompletesArchive(t *testing.T) {
	l := explicitWorkspace(t)
	dir := l.Repos["a"]
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/repo.git")
	reviewCloseReady(t, l, false)
	for _, args := range [][]string{{"set", "integration", "review", "pr"}, {"merge-deltas", "review"}, {"close", "review"}} {
		if out, err := runOnto(t, append(args, "--dir", l.ConfigRoot)...); err != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
	}
	archive, _, err := locateArchive(l.ConfigRoot, "review")
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := integrationrecord.Load(archive, "review")
	if err != nil {
		t.Fatal(err)
	}
	head := r.Repositories[0].SourceCommit
	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "merge", "--squash", "review")
	commitAll(t, dir, "squash")
	squash, _ := resolveCommit(dir, "HEAD")
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	response := fmt.Sprintf(`{"html_url":"https://github.com/acme/repo/pull/1","number":1,"state":"closed","merged":true,"merge_commit_sha":%q,"head":{"sha":%q},"base":{"ref":"main","repo":{"full_name":"acme/repo"}}}`, squash, head)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nprintf '%s' '"+response+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	args := []string{"complete-integration", "review", "--receipt", "squash-pr:https://github.com/acme/repo/pull/1", "--dir", l.ConfigRoot}
	for i := 0; i < 2; i++ {
		if out, err := runOnto(t, args...); err != nil {
			t.Fatalf("completion: %s %v", out, err)
		}
	}
	r, _, err = integrationrecord.Load(archive, "review")
	if err != nil || r.Status != integrationrecord.StatusComplete || r.Repositories[0].Receipt != args[3]+"#"+squash {
		t.Fatalf("record: %+v %v", r, err)
	}
}
