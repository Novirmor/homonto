package hostruntime

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GitLab is the GitLab.com provider (self-managed GitLab is out of scope for
// 1.0). Every call goes through `glab api --hostname`, so the coordinator
// keeps the host-CLI-only posture (ADR 0047). Mapping decisions are in
// docs/guides/hosting-differences.md.
type GitLab struct{}

// GitLabHost is the only supported GitLab host.
const GitLabHost = "gitlab.com"

func (GitLab) ID() string    { return "gitlab" }
func (GitLab) Label() string { return "GitLab" }
func (GitLab) CLI() string   { return "glab" }

var (
	gitlabTarget  = regexp.MustCompile(`^https://([^/]+)/(.+)/-/(issues|merge_requests)/([1-9][0-9]*)$`)
	gitlabOrigin  = regexp.MustCompile(`^(?:https://|ssh://git@|git@)([a-z0-9.-]+)[/:]([^\s:]+?)(?:\.git)?$`)
	gitlabSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	gitlabUser    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
)

// gitlabPath accepts a project path with nested groups: group[/subgroup...]/project.
func gitlabPath(v any) bool {
	s, ok := v.(string)
	if !ok || byteLen(s) > 1024 {
		return false
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || len(parts) > 21 {
		return false
	}
	for _, p := range parts {
		if !gitlabSegment.MatchString(p) || strings.HasSuffix(p, ".git") || strings.HasSuffix(p, ".atom") || strings.Contains(p, "..") {
			return false
		}
	}
	return true
}

func (GitLab) ParseTarget(raw string) (Target, error) {
	if byteLen(raw) > 2048 {
		return Target{}, errors.New("invalid GitLab URL")
	}
	m := gitlabTarget.FindStringSubmatch(raw)
	if m == nil || !gitlabPath(m[2]) {
		return Target{}, errors.New("expected a plain HTTPS GitLab issue or merge request URL")
	}
	if m[1] != GitLabHost {
		return Target{}, errors.New("GitLab targets must be on gitlab.com; self-managed GitLab is not supported")
	}
	n, err := strconv.ParseInt(m[4], 10, 64)
	if err != nil || n > (1<<53)-1 {
		return Target{}, errors.New("expected a plain HTTPS GitLab issue or merge request URL")
	}
	return Target{Host: m[1], Repo: m[2], Number: n, Type: m[3], Change: m[3] == "merge_requests", URL: raw}, nil
}

func (GitLab) ValidateItem(in InputItem, t Target) error { return GitHub{}.ValidateItem(in, t) }

func (GitLab) Origin(remote string) (string, string, error) {
	m := gitlabOrigin.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil || m[1] != GitLabHost || !gitlabPath(m[2]) || strings.ContainsAny(m[2], "?#@%") {
		return "", "", errors.New("unsupported origin URL")
	}
	return m[1], m[2], nil
}

func gitlabArgv(host, endpoint, method string, paginated bool) []string {
	argv := []string{"glab", "api", "--hostname", host, "--method", method}
	if paginated {
		argv = append(argv, "--paginate")
	}
	argv = append(argv, endpoint)
	if method == "POST" {
		argv = append(argv, "--input", "-", "-H", "Content-Type: application/json")
	}
	return argv
}

func gitlabGet(x Transport, host, endpoint string, paginated bool) (any, error) {
	out, err := x.Run(gitlabArgv(host, endpoint, "GET", paginated), nil)
	if err != nil {
		return nil, err
	}
	return decode(out)
}

func gitlabProject(name string) string { return "projects/" + url.PathEscape(name) }

func (GitLab) Repository(x Transport, host, name string) (Repo, error) {
	v, err := gitlabGet(x, host, gitlabProject(name), false)
	if err != nil {
		return Repo{}, err
	}
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	full, _ := text(m["path_with_namespace"])
	if m == nil || !idOK || !gitlabPath(m["path_with_namespace"]) || m["web_url"] != "https://"+host+"/"+full {
		return Repo{}, errors.New("invalid repository identity")
	}
	return Repo{Host: host, ID: NumberID(id), Name: full}, nil
}

func (GitLab) Actor(x Transport, host string) (Actor, error) {
	v, err := gitlabGet(x, host, "user", false)
	if err != nil {
		return Actor{}, err
	}
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	login, loginOK := text(m["username"])
	if m == nil || !idOK || !loginOK || !gitlabUser.MatchString(login) {
		return Actor{}, errors.New("invalid authenticated actor")
	}
	return Actor{ID: NumberID(id), Login: login}, nil
}

func gitlabKind(t Target) string {
	if t.Change {
		return "merge_requests"
	}
	return "issues"
}

func (GitLab) Destination(x Transport, t Target, r Repo) (Snapshot, error) {
	project, _ := strconv.ParseInt(string(r.ID), 10, 64)
	v, err := gitlabGet(x, t.Host, fmt.Sprintf("projects/%d/%s/%d", project, gitlabKind(t), t.Number), false)
	if err != nil {
		return Snapshot{}, err
	}
	m, _ := record(v)
	bad := errors.New("invalid destination snapshot")
	if m == nil {
		return Snapshot{}, bad
	}
	id, idOK := safeID(m["id"])
	iid, iidOK := safeID(m["iid"])
	state, _ := text(m["state"])
	title, titleOK := text(m["title"])
	updated, updatedOK := text(m["updated_at"])
	var body *string
	switch b := m["description"].(type) {
	case nil:
	case string:
		if byteLen(b) > 1048576 {
			return Snapshot{}, bad
		}
		body = &b
	default:
		return Snapshot{}, bad
	}
	locked := false
	switch l := m["discussion_locked"].(type) {
	case nil:
	case bool:
		locked = l
	default:
		return Snapshot{}, bad
	}
	_, perr := time.Parse(time.RFC3339, updated)
	if !idOK || !iidOK || iid != t.Number || !titleOK || byteLen(title) > 8192 || !updatedOK || perr != nil ||
		m["web_url"] != fmt.Sprintf("https://%s/%s/-/%s/%d", t.Host, r.Name, gitlabKind(t), t.Number) || !numberEqual(m["project_id"], r.ID) {
		return Snapshot{}, bad
	}
	snap := Snapshot{ID: NumberID(id), Number: t.Number, Title: title, Body: body, UpdatedAt: updated, Locked: locked}
	if !t.Change {
		if state != "opened" && state != "closed" {
			return Snapshot{}, bad
		}
		snap.State = map[string]string{"opened": "open", "closed": "closed"}[state]
		return snap, nil
	}
	if state != "opened" && state != "closed" && state != "merged" && state != "locked" {
		return Snapshot{}, bad
	}
	snap.State = "closed"
	if state == "opened" {
		snap.State = "open"
	}
	snap.Merged = state == "merged"
	refs, refsOK := record(m["diff_refs"])
	isDraft, draftOK := boolean(m["draft"])
	if !refsOK || !isOID(refs["base_sha"]) || !isOID(refs["start_sha"]) || !isOID(refs["head_sha"]) {
		return Snapshot{}, errors.New("merge request diff is not ready (diff_refs missing); retry shortly")
	}
	if !draftOK || m["sha"] != refs["head_sha"] || !numberEqual(m["target_project_id"], r.ID) {
		return Snapshot{}, errors.New("invalid merge request snapshot")
	}
	// GitLab's start_sha is the target tip the diff was computed against, the
	// counterpart of GitHub's base.sha; base_sha (the merge base) is freshness
	// evidence too.
	snap.BaseOID, snap.HeadOID = refs["start_sha"].(string), refs["head_sha"].(string)
	snap.Draft = isDraft
	snap.Extra = refs["base_sha"].(string)
	return snap, nil
}

func (GitLab) CanonicalURL(t Target, repo string) string {
	return fmt.Sprintf("https://%s/%s/-/%s/%d", t.Host, repo, gitlabKind(t), t.Number)
}

// gitlabReviewerState maps a formal review event onto draft_notes/bulk_publish.
func gitlabReviewerState(event string) string {
	if event == EventRequestChanges {
		return "requested_changes"
	}
	return "reviewed"
}

func (GitLab) Payload(i *Item) (string, error) {
	if i.Kind == KindPRReview {
		return marshal(struct {
			Note          string `json:"note"`
			ReviewerState string `json:"reviewer_state"`
		}{i.Body, gitlabReviewerState(i.ReviewEvent)}, false), nil
	}
	return marshal(struct {
		Body string `json:"body"`
	}{i.Body}, false), nil
}

func gitlabBase(i *Item) string {
	project, _ := strconv.ParseInt(string(i.Snapshot.Repo.ID), 10, 64)
	return fmt.Sprintf("projects/%d/%s/%d", project, gitlabKind(i.Target), i.Target.Number)
}

func gitlabEndpoint(i *Item) string {
	if i.Kind == KindPRReview {
		return gitlabBase(i) + "/draft_notes/bulk_publish"
	}
	return gitlabBase(i) + "/notes"
}

func (GitLab) PublishCommand(i *Item) []string {
	return gitlabArgv(i.Target.Host, gitlabEndpoint(i), "POST", false)
}

func gitlabNoteType(t Target) string {
	if t.Change {
		return "MergeRequest"
	}
	return "Issue"
}

// gitlabNote validates a note and reports whether it is one this item would
// have published. Notes carry no web URL; the receipt URL is the target's with
// GitLab's #note_<id> anchor.
func gitlabNote(v any, i *Item) (Receipt, bool, error) {
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	author, authorOK := record(m["author"])
	var authorIDOK, loginOK bool
	if authorOK {
		_, authorIDOK = safeID(author["id"])
		_, loginOK = text(author["username"])
	}
	system, systemOK := boolean(m["system"])
	if _, bodyOK := text(m["body"]); m == nil || !idOK || !authorOK || !authorIDOK || !loginOK || !bodyOK || !systemOK {
		return Receipt{}, false, errors.New("invalid remote publication record")
	}
	if system || m["body"] != i.Body || !numberEqual(author["id"], i.Snapshot.Actor.ID) || author["username"] != i.Snapshot.Actor.Login {
		return Receipt{}, false, nil
	}
	if !numberEqual(m["noteable_iid"], NumberID(i.Target.Number)) || m["noteable_type"] != gitlabNoteType(i.Target) {
		return Receipt{}, false, errors.New("invalid receipt destination")
	}
	return Receipt{ID: NumberID(id), URL: fmt.Sprintf("%s#note_%d", i.Target.URL, id)}, true, nil
}

func (GitLab) Existing(x Transport, i *Item) ([]Receipt, error) {
	v, err := gitlabGet(x, i.Target.Host, gitlabBase(i)+"/notes?per_page=100&sort=asc&order_by=created_at", true)
	if err != nil {
		return nil, err
	}
	notes, ok := v.([]any)
	if !ok || len(notes) > 100000 {
		return nil, errors.New("invalid paginated destination response")
	}
	return matchReceipts(notes, func(v any) (bool, error) {
		_, ok, err := gitlabNote(v, i)
		return ok, err
	}, func(v any) (Receipt, error) {
		r, _, err := gitlabNote(v, i)
		return r, err
	})
}

// Preflight refuses a formal review while the user has pending draft notes:
// bulk_publish would publish all of them along with the review.
func (GitLab) Preflight(x Transport, i *Item) error {
	if i.Kind != KindPRReview {
		return nil
	}
	v, err := gitlabGet(x, i.Target.Host, gitlabBase(i)+"/draft_notes", false)
	if err != nil {
		return err
	}
	if list, ok := v.([]any); !ok || len(list) != 0 {
		return errors.New("pending GitLab draft notes exist on this merge request; publish or delete them before a formal review")
	}
	return nil
}

func (g GitLab) Publish(x Transport, i *Item) (Receipt, error) {
	payload := i.Payload
	out, err := x.Run(g.PublishCommand(i), &payload)
	if err != nil {
		return Receipt{}, err
	}
	if i.Kind == KindPRReview {
		// bulk_publish returns no note; the summary note is the receipt.
		matches, err := g.Existing(x, i)
		if err != nil {
			return Receipt{}, err
		}
		if len(matches) != 1 {
			return Receipt{}, errors.New("review published without a single matching summary note")
		}
		return matches[0], nil
	}
	v, err := decode(out)
	if err != nil {
		return Receipt{}, err
	}
	r, ok, err := gitlabNote(v, i)
	if err != nil {
		return Receipt{}, err
	}
	if !ok {
		return Receipt{}, errors.New("invalid receipt actor or body")
	}
	return r, nil
}
