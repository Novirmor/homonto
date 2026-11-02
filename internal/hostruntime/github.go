package hostruntime

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GitHub is the github.com / GHES provider. Every call goes through
// `gh api --hostname`, so the coordinator never needs network access.
type GitHub struct{}

func (GitHub) ID() string    { return "github" }
func (GitHub) Label() string { return "GitHub" }
func (GitHub) CLI() string   { return "gh" }

var (
	githubTarget = regexp.MustCompile(`^https://([^/]+)/([^/]+/[^/]+)/(issues|pull)/([1-9][0-9]*)$`)
	githubOrigin = regexp.MustCompile(`^(?:https://|ssh://git@|git@)([a-z0-9.-]+)[/:]([^\s/:]+/[^\s/]+?)(?:\.git)?$`)
	githubOwner  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	githubRepo   = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
)

// githubName accepts OWNER/REPO where REPO is not "." or "..".
func githubName(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	owner, repo, found := strings.Cut(s, "/")
	return found && githubOwner.MatchString(owner) && githubRepo.MatchString(repo) && repo != "." && repo != ".."
}

func (GitHub) ParseTarget(raw string) (Target, error) {
	if byteLen(raw) > 2048 {
		return Target{}, errors.New("invalid GitHub URL")
	}
	m := githubTarget.FindStringSubmatch(raw)
	if m == nil || !hostOK(m[1]) || !githubName(m[2]) {
		return Target{}, errors.New("expected a plain HTTPS issue or pull URL")
	}
	n, err := strconv.ParseInt(m[4], 10, 64)
	if err != nil || n > (1<<53)-1 {
		return Target{}, errors.New("expected a plain HTTPS issue or pull URL")
	}
	return Target{Host: m[1], Repo: m[2], Number: n, Type: m[3], Change: m[3] == "pull", URL: raw}, nil
}

func (GitHub) ValidateItem(in InputItem, t Target) error {
	if in.Kind == KindIssueComment {
		return check(!t.Change && in.BaseOID == "" && in.HeadOID == "" && in.ReviewEvent == "", "issue fields must be empty")
	}
	if err := check(t.Change && isOID(in.BaseOID) && isOID(in.HeadOID), "PR requires reviewed base/head OIDs"); err != nil {
		return err
	}
	if in.Kind == KindPRReview {
		return check(in.ReviewEvent == EventComment || in.ReviewEvent == EventRequestChanges, "formal reviews support COMMENT or REQUEST_CHANGES only")
	}
	return check(in.ReviewEvent == "", "formal reviews support COMMENT or REQUEST_CHANGES only")
}

func (GitHub) Origin(remote string) (string, string, error) {
	m := githubOrigin.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil || !hostOK(m[1]) || !githubName(m[2]) || strings.ContainsAny(m[2], "?#@%") {
		return "", "", errors.New("unsupported origin URL")
	}
	return m[1], m[2], nil
}

func githubArgv(host, endpoint, method string, paginated bool) []string {
	argv := []string{"gh", "api", "--hostname", host, "--method", method, "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28"}
	if paginated {
		argv = append(argv, "--paginate", "--slurp")
	}
	argv = append(argv, endpoint)
	if method == "POST" {
		argv = append(argv, "--input", "-")
	}
	return argv
}

func githubGet(x Transport, host, endpoint string, paginated bool) (any, error) {
	out, err := x.Run(githubArgv(host, endpoint, "GET", paginated), nil)
	if err != nil {
		return nil, err
	}
	return decode(out)
}

func (GitHub) Repository(x Transport, host, name string) (Repo, error) {
	v, err := githubGet(x, host, "repos/"+name, false)
	if err != nil {
		return Repo{}, err
	}
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	full, _ := text(m["full_name"])
	if m == nil || !idOK || !githubName(m["full_name"]) || m["html_url"] != "https://"+host+"/"+full {
		return Repo{}, errors.New("invalid repository identity")
	}
	return Repo{Host: host, ID: NumberID(id), Name: full}, nil
}

func (GitHub) Actor(x Transport, host string) (Actor, error) {
	v, err := githubGet(x, host, "user", false)
	if err != nil {
		return Actor{}, err
	}
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	login, loginOK := text(m["login"])
	if m == nil || !idOK || !loginOK || !githubOwner.MatchString(login) {
		return Actor{}, errors.New("invalid authenticated actor")
	}
	return Actor{ID: NumberID(id), Login: login}, nil
}

func (GitHub) Destination(x Transport, t Target, r Repo) (Snapshot, error) {
	kind := "issues"
	if t.Change {
		kind = "pulls"
	}
	v, err := githubGet(x, t.Host, fmt.Sprintf("repos/%s/%s/%d", r.Name, kind, t.Number), false)
	if err != nil {
		return Snapshot{}, err
	}
	m, _ := record(v)
	bad := errors.New("invalid destination snapshot")
	if m == nil {
		return Snapshot{}, bad
	}
	id, idOK := safeID(m["id"])
	number, numOK := safeID(m["number"])
	state, _ := text(m["state"])
	title, titleOK := text(m["title"])
	updated, updatedOK := text(m["updated_at"])
	locked, lockedOK := boolean(m["locked"])
	var body *string
	switch b := m["body"].(type) {
	case nil:
	case string:
		if byteLen(b) > 262144 {
			return Snapshot{}, bad
		}
		body = &b
	default:
		return Snapshot{}, bad
	}
	if _, present := m["body"]; !present {
		return Snapshot{}, bad
	}
	if _, perr := time.Parse(time.RFC3339, updated); !idOK || !numOK || number != t.Number || (state != "open" && state != "closed") ||
		!titleOK || byteLen(title) > 8192 || !updatedOK || perr != nil || !lockedOK ||
		m["html_url"] != fmt.Sprintf("https://%s/%s/%s/%d", t.Host, r.Name, t.Type, t.Number) {
		return Snapshot{}, bad
	}
	snap := Snapshot{ID: NumberID(id), Number: t.Number, State: state, Title: title, Body: body, UpdatedAt: updated, Locked: locked}
	if !t.Change {
		if _, ok := m["pull_request"]; ok {
			return Snapshot{}, errors.New("issue URL resolves to a pull request")
		}
		return snap, nil
	}
	base, baseOK := record(m["base"])
	head, headOK := record(m["head"])
	var baseRepo map[string]any
	if baseOK {
		baseRepo, _ = record(base["repo"])
	}
	merged, mergedOK := boolean(m["merged"])
	isDraft, draftOK := boolean(m["draft"])
	if !baseOK || !isOID(base["sha"]) || !headOK || !isOID(head["sha"]) || baseRepo == nil || !numberEqual(baseRepo["id"], r.ID) || !mergedOK || !draftOK {
		return Snapshot{}, errors.New("invalid pull snapshot")
	}
	snap.BaseOID, snap.HeadOID = base["sha"].(string), head["sha"].(string)
	snap.Merged, snap.Draft = merged, isDraft
	return snap, nil
}

func (GitHub) CanonicalURL(t Target, repo string) string {
	return fmt.Sprintf("https://%s/%s/%s/%d", t.Host, repo, t.Type, t.Number)
}

func (GitHub) Payload(i *Item) (string, error) {
	if i.Kind == KindPRReview {
		return marshal(struct {
			CommitID string `json:"commit_id"`
			Event    string `json:"event"`
			Body     string `json:"body"`
		}{i.HeadOID, i.ReviewEvent, i.Body}, false), nil
	}
	return marshal(struct {
		Body string `json:"body"`
	}{i.Body}, false), nil
}

func githubEndpoint(i *Item) string {
	if i.Kind == KindPRReview {
		return fmt.Sprintf("repos/%s/pulls/%d/reviews", i.Snapshot.Repo.Name, i.Target.Number)
	}
	return fmt.Sprintf("repos/%s/issues/%d/comments", i.Snapshot.Repo.Name, i.Target.Number)
}

func (GitHub) PublishCommand(i *Item) []string {
	return githubArgv(i.Target.Host, githubEndpoint(i), "POST", false)
}

func githubReviewState(event string) string {
	if event == EventComment {
		return "COMMENTED"
	}
	return "CHANGES_REQUESTED"
}

func githubReceipt(v any, i *Item) (Receipt, error) {
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	user, userOK := record(m["user"])
	if m == nil || !idOK || !userOK || !numberEqual(user["id"], i.Snapshot.Actor.ID) || user["login"] != i.Snapshot.Actor.Login || m["body"] != i.Body {
		return Receipt{}, errors.New("invalid receipt actor or body")
	}
	base := fmt.Sprintf("https://%s/%s/%s/%d", i.Target.Host, i.Snapshot.Repo.Name, i.Target.Type, i.Target.Number)
	anchor := "issuecomment-"
	if i.Kind == KindPRReview {
		anchor = "pullrequestreview-"
	}
	if m["html_url"] != fmt.Sprintf("%s#%s%d", base, anchor, id) {
		return Receipt{}, errors.New("invalid receipt destination URL")
	}
	apiRoot := "https://" + i.Target.Host + "/api/v3"
	if i.Target.Host == "github.com" {
		apiRoot = "https://api.github.com"
	}
	if i.Kind == KindPRReview {
		if m["commit_id"] != i.HeadOID || m["state"] != githubReviewState(i.ReviewEvent) {
			return Receipt{}, errors.New("invalid review receipt")
		}
		if m["pull_request_url"] != fmt.Sprintf("%s/repos/%s/pulls/%d", apiRoot, i.Snapshot.Repo.Name, i.Target.Number) {
			return Receipt{}, errors.New("invalid review destination")
		}
	} else if m["issue_url"] != fmt.Sprintf("%s/repos/%s/issues/%d", apiRoot, i.Snapshot.Repo.Name, i.Target.Number) {
		return Receipt{}, errors.New("invalid comment destination")
	}
	return Receipt{ID: NumberID(id), URL: m["html_url"].(string)}, nil
}

// Preflight has nothing to check on GitHub.
func (GitHub) Preflight(Transport, *Item) error { return nil }

func (GitHub) Publish(x Transport, i *Item) (Receipt, error) {
	payload := i.Payload
	out, err := x.Run(githubArgv(i.Target.Host, githubEndpoint(i), "POST", false), &payload)
	if err != nil {
		return Receipt{}, err
	}
	v, err := decode(out)
	if err != nil {
		return Receipt{}, err
	}
	return githubReceipt(v, i)
}

func (GitHub) Existing(x Transport, i *Item) ([]Receipt, error) {
	v, err := githubGet(x, i.Target.Host, githubEndpoint(i)+"?per_page=100", true)
	if err != nil {
		return nil, err
	}
	pages, ok := v.([]any)
	if !ok || len(pages) == 0 || len(pages) > 1000 {
		return nil, errors.New("invalid paginated destination response")
	}
	var records []any
	for _, p := range pages {
		page, ok := p.([]any)
		if !ok || len(page) > 100 {
			return nil, errors.New("invalid paginated destination response")
		}
		records = append(records, page...)
	}
	return matchReceipts(records, func(v any) (bool, error) {
		m, _ := record(v)
		_, idOK := safeID(m["id"])
		user, userOK := record(m["user"])
		var userIDOK, loginOK bool
		if userOK {
			_, userIDOK = safeID(user["id"])
			_, loginOK = text(user["login"])
		}
		if _, bodyOK := text(m["body"]); m == nil || !idOK || !userOK || !userIDOK || !loginOK || !bodyOK {
			return false, errors.New("invalid remote publication record")
		}
		if m["body"] != i.Body || !numberEqual(user["id"], i.Snapshot.Actor.ID) || user["login"] != i.Snapshot.Actor.Login {
			return false, nil
		}
		if i.Kind == KindPRReview && (m["commit_id"] != i.HeadOID || m["state"] != githubReviewState(i.ReviewEvent)) {
			return false, nil
		}
		return true, nil
	}, func(v any) (Receipt, error) { return githubReceipt(v, i) })
}

// matchReceipts filters remote records and validates each match as a receipt,
// de-duplicated by receipt ID in first-seen order.
func matchReceipts(records []any, match func(any) (bool, error), receipt func(any) (Receipt, error)) ([]Receipt, error) {
	var found []Receipt
	seen := map[ID]int{}
	for _, v := range records {
		ok, err := match(v)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		r, err := receipt(v)
		if err != nil {
			return nil, err
		}
		if n, dup := seen[r.ID]; dup {
			found[n] = r
			continue
		}
		seen[r.ID] = len(found)
		found = append(found, r)
	}
	return found, nil
}
