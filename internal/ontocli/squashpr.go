package ontocli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/Novirmor/homonto/internal/integrationrecord"
)

var githubPRURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*)/pull/([1-9][0-9]*)$`)
var githubOrigin = regexp.MustCompile(`^(?:https://github\.com/|ssh://git@github\.com/|git@github\.com:)([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*?)(?:\.git)?$`)

func runGitHubAPI(ctx context.Context, host, endpoint, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "--hostname", host, "--method", "GET", "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", endpoint)
	cmd.Dir = dir
	return cmd.Output()
}

// A squash has no source parent. Bind it to GitHub's merged PR, the local
// origin and receiving history, and require the entire resulting tree to be
// the verified candidate's tree. Tree equality alone does not prove provenance.
func validateSquashPR(root, dir, receipt string, entry integrationrecord.Entry) (string, error) {
	raw := strings.TrimPrefix(receipt, "squash-pr:")
	url, recorded, _ := strings.Cut(raw, "#")
	target := githubPRURL.FindStringSubmatch(url)
	if target == nil {
		return "", fmt.Errorf("squash-pr requires a canonical GitHub pull request URL")
	}
	number, err := strconv.ParseInt(target[2], 10, 64)
	if err != nil || number > (1<<53)-1 {
		return "", fmt.Errorf("invalid GitHub pull request number")
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	origin, err := gitAt(ctx, dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("cannot read source origin: %w", err)
	}
	originParts := githubOrigin.FindStringSubmatch(strings.TrimSpace(string(origin)))
	if originParts == nil || !strings.EqualFold(originParts[1], target[1]) {
		return "", fmt.Errorf("PR repository does not match source origin")
	}
	repo := originParts[1]
	ctx2, cancel2 := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel2()
	out, err := runGitHubAPI(ctx2, "github.com", fmt.Sprintf("repos/%s/pulls/%d", target[1], number), dir)
	if err != nil {
		return "", fmt.Errorf("cannot read GitHub PR: %w", err)
	}
	var pr struct {
		HTMLURL     string `json:"html_url"`
		Number      int64  `json:"number"`
		State       string `json:"state"`
		Merged      bool   `json:"merged"`
		MergeCommit string `json:"merge_commit_sha"`
		Head        struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	}
	if err := json.Unmarshal(out, &pr); err != nil || pr.HTMLURL != url || pr.Number != number || pr.State != "closed" || !pr.Merged || pr.Base.Ref != entry.BaseBranch || !strings.EqualFold(pr.Base.Repo.FullName, repo) || !fullCommitID.MatchString(pr.Head.SHA) || !fullCommitID.MatchString(pr.MergeCommit) {
		return "", fmt.Errorf("GitHub PR is not a merged PR for the recorded repository and base branch")
	}
	if recorded != "" && recorded != pr.MergeCommit {
		return "", fmt.Errorf("recorded squash commit differs from GitHub PR")
	}
	if err := integrationCandidateIntact(root, dir, entry, pr.Head.SHA); err != nil {
		return "", fmt.Errorf("PR head is not the verified candidate: %w", err)
	}
	sha, err := resolveCommit(dir, pr.MergeCommit)
	if err != nil {
		return "", err
	}
	parents, err := commitParents(dir, sha)
	if err != nil || len(parents) != 1 {
		return "", fmt.Errorf("PR merge commit is not a single-parent squash commit")
	}
	if err := isAncestor(dir, entry.BaseCommit, parents[0]); err != nil {
		return "", fmt.Errorf("squash parent does not descend from recorded base: %w", err)
	}
	if err := isAncestor(dir, sha, "refs/heads/"+entry.BaseBranch); err != nil {
		return "", fmt.Errorf("squash commit is not on receiving branch: %w", err)
	}
	paths, err := changedPaths(dir, pr.Head.SHA, sha)
	if err != nil || len(paths) != 0 {
		return "", fmt.Errorf("squash tree differs from verified PR head")
	}
	return "squash-pr:" + url + "#" + sha, nil
}
