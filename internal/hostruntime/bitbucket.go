package hostruntime

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Bitbucket is the Bitbucket Cloud provider. Its host CLI is homonto's own
// `homonto host bitbucket api` (ADR 0091). Bitbucket Cloud has no issue
// tracker (retired 2026), so only pull request items exist; formal reviews
// are a comment plus, for REQUEST_CHANGES, the request-changes call
// (docs/guides/hosting-differences.md).
type Bitbucket struct{}

// BitbucketHost is the web host of Bitbucket Cloud.
const BitbucketHost = "bitbucket.org"

func (Bitbucket) ID() string    { return "bitbucket" }
func (Bitbucket) Label() string { return "Bitbucket" }
func (Bitbucket) CLI() []string { return []string{"homonto", "host", "bitbucket"} }

var (
	bitbucketTarget = regexp.MustCompile(`^https://([^/]+)/([^/]+/[^/]+)/pull-requests/([1-9][0-9]*)$`)
	bitbucketOrigin = regexp.MustCompile(`^(?:https://(?:[A-Za-z0-9_.-]+@)?|ssh://git@|git@)([a-z0-9.-]+)[/:]([^\s/:]+/[^\s/]+?)(?:\.git)?$`)
	bitbucketSlug   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	bitbucketHash   = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

func bitbucketName(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	ws, repo, found := strings.Cut(s, "/")
	return found && bitbucketSlug.MatchString(ws) && bitbucketSlug.MatchString(repo) && ws != "." && ws != ".." && repo != "." && repo != ".."
}

func (Bitbucket) ParseTarget(raw string) (Target, error) {
	if byteLen(raw) > 2048 {
		return Target{}, errors.New("invalid Bitbucket URL")
	}
	m := bitbucketTarget.FindStringSubmatch(raw)
	if m == nil || m[1] != BitbucketHost || !bitbucketName(m[2]) {
		return Target{}, errors.New("expected a plain HTTPS bitbucket.org pull request URL (Bitbucket Cloud has no issue tracker)")
	}
	n, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil || n > (1<<53)-1 {
		return Target{}, errors.New("expected a plain HTTPS bitbucket.org pull request URL (Bitbucket Cloud has no issue tracker)")
	}
	return Target{Host: m[1], Repo: m[2], Number: n, Type: "pull-requests", Change: true, URL: raw}, nil
}

func (Bitbucket) ValidateItem(in InputItem, t Target) error {
	if in.Kind == KindIssueComment {
		return errors.New("Bitbucket Cloud has no issue tracker; issue comments are unsupported")
	}
	return GitHub{}.ValidateItem(in, t)
}

func (Bitbucket) Origin(remote string) (string, string, error) {
	m := bitbucketOrigin.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil || m[1] != BitbucketHost || !bitbucketName(m[2]) || strings.ContainsAny(m[2], "?#@%") {
		return "", "", errors.New("unsupported origin URL")
	}
	return m[1], m[2], nil
}

func bitbucketArgv(endpoint, method string, paginated bool) []string {
	argv := []string{"homonto", "host", "bitbucket", "api", "--method", method}
	if paginated {
		argv = append(argv, "--paginate")
	}
	argv = append(argv, endpoint)
	if method == "POST" {
		argv = append(argv, "--input", "-")
	}
	return argv
}

func bitbucketGet(x Transport, endpoint string, paginated bool) (any, error) {
	out, err := x.Run(bitbucketArgv(endpoint, "GET", paginated), nil)
	if err != nil {
		return nil, err
	}
	return decode(out)
}

func uuidText(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && len(s) >= 3 && len(s) <= 64 && strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
}

func htmlHref(m map[string]any) any {
	links, _ := record(m["links"])
	html, _ := record(links["html"])
	return html["href"]
}

func (Bitbucket) Repository(x Transport, host, name string) (Repo, error) {
	v, err := bitbucketGet(x, "repositories/"+name, false)
	if err != nil {
		return Repo{}, err
	}
	m, _ := record(v)
	id, idOK := uuidText(m["uuid"])
	full, _ := text(m["full_name"])
	if m == nil || !idOK || !bitbucketName(m["full_name"]) || !strings.EqualFold(full, name) || htmlHref(m) != "https://"+host+"/"+full {
		return Repo{}, errors.New("invalid repository identity")
	}
	return Repo{Host: host, ID: StringID(id), Name: full}, nil
}

func (Bitbucket) Actor(x Transport, host string) (Actor, error) {
	v, err := bitbucketGet(x, "user", false)
	if err != nil {
		return Actor{}, err
	}
	m, _ := record(v)
	id, idOK := uuidText(m["uuid"])
	login, loginOK := text(m["nickname"])
	if m == nil || !idOK || !loginOK || login == "" || byteLen(login) > 256 {
		return Actor{}, errors.New("invalid authenticated actor")
	}
	return Actor{ID: StringID(id), Login: login}, nil
}

// fullHash expands a possibly abbreviated commit hash through the repository
// that holds it; freshness always compares full 40-hex object IDs.
func fullHash(x Transport, repo string, hash any) (string, error) {
	h, ok := hash.(string)
	if !ok || !bitbucketHash.MatchString(h) {
		return "", errors.New("invalid pull request commit")
	}
	if len(h) == 40 {
		return h, nil
	}
	v, err := bitbucketGet(x, fmt.Sprintf("repositories/%s/commit/%s", repo, h), false)
	if err != nil {
		return "", err
	}
	m, _ := record(v)
	full, _ := text(m["hash"])
	if !isOID(full) || !strings.HasPrefix(full, h) {
		return "", errors.New("invalid pull request commit")
	}
	return full, nil
}

func (Bitbucket) Destination(x Transport, t Target, r Repo) (Snapshot, error) {
	v, err := bitbucketGet(x, fmt.Sprintf("repositories/%s/pullrequests/%d", r.Name, t.Number), false)
	if err != nil {
		return Snapshot{}, err
	}
	m, _ := record(v)
	bad := errors.New("invalid destination snapshot")
	if m == nil {
		return Snapshot{}, bad
	}
	number, numOK := safeID(m["id"])
	state, _ := text(m["state"])
	title, titleOK := text(m["title"])
	updated, updatedOK := text(m["updated_on"])
	isDraft, draftOK := boolean(m["draft"])
	var body *string
	if summary, ok := record(m["summary"]); ok {
		if raw, ok := text(summary["raw"]); ok {
			if byteLen(raw) > 262144 {
				return Snapshot{}, bad
			}
			body = &raw
		}
	}
	_, perr := time.Parse(time.RFC3339Nano, updated)
	if !numOK || number != t.Number || !titleOK || byteLen(title) > 8192 || !updatedOK || perr != nil || !draftOK ||
		(state != "OPEN" && state != "MERGED" && state != "DECLINED" && state != "SUPERSEDED") ||
		htmlHref(m) != fmt.Sprintf("https://%s/%s/pull-requests/%d", t.Host, r.Name, t.Number) {
		return Snapshot{}, bad
	}
	src, _ := record(m["source"])
	dst, _ := record(m["destination"])
	srcRepo, _ := record(src["repository"])
	dstRepo, _ := record(dst["repository"])
	srcCommit, _ := record(src["commit"])
	dstCommit, _ := record(dst["commit"])
	srcName, _ := text(srcRepo["full_name"])
	dstID, dstIDOK := uuidText(dstRepo["uuid"])
	if src == nil || dst == nil || srcCommit == nil || dstCommit == nil || !bitbucketName(srcRepo["full_name"]) || !dstIDOK || StringID(dstID) != r.ID {
		return Snapshot{}, errors.New("invalid pull snapshot")
	}
	head, err := fullHash(x, srcName, srcCommit["hash"])
	if err != nil {
		return Snapshot{}, err
	}
	base, err := fullHash(x, r.Name, dstCommit["hash"])
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{ID: NumberID(number), Number: t.Number, State: "closed", Title: title, Body: body, UpdatedAt: updated,
		BaseOID: base, HeadOID: head, Merged: state == "MERGED", Draft: isDraft}
	if state == "OPEN" {
		snap.State = "open"
	}
	return snap, nil
}

func (Bitbucket) CanonicalURL(t Target, repo string) string {
	return fmt.Sprintf("https://%s/%s/pull-requests/%d", t.Host, repo, t.Number)
}

func (Bitbucket) Payload(i *Item) (string, error) {
	type content struct {
		Raw string `json:"raw"`
	}
	return marshal(struct {
		Content content `json:"content"`
	}{content{i.Body}}, false), nil
}

func bitbucketPR(i *Item) string {
	return fmt.Sprintf("repositories/%s/pullrequests/%d", i.Snapshot.Repo.Name, i.Target.Number)
}

func (Bitbucket) PublishCommand(i *Item) []string {
	return bitbucketArgv(bitbucketPR(i)+"/comments", "POST", false)
}

func (Bitbucket) Preflight(Transport, *Item) error { return nil }

// bitbucketComment validates a comment and reports whether it is a top-level
// comment this item would have published.
func bitbucketComment(v any, i *Item) (Receipt, bool, error) {
	m, _ := record(v)
	id, idOK := safeID(m["id"])
	user, userOK := record(m["user"])
	content, contentOK := record(m["content"])
	var raw string
	rawOK := false
	if contentOK {
		raw, rawOK = text(content["raw"])
	}
	deleted, _ := boolean(m["deleted"])
	if m == nil || !idOK || !userOK || !rawOK {
		return Receipt{}, false, errors.New("invalid remote publication record")
	}
	if m["inline"] != nil || deleted || m["parent"] != nil || raw != i.Body || StringID(fmt.Sprint(user["uuid"])) != i.Snapshot.Actor.ID {
		return Receipt{}, false, nil
	}
	pr, _ := record(m["pullrequest"])
	want := fmt.Sprintf("https://%s/%s/pull-requests/%d#comment-%d", i.Target.Host, i.Snapshot.Repo.Name, i.Target.Number, id)
	if pr == nil || !numberEqual(pr["id"], NumberID(i.Target.Number)) || htmlHref(m) != want {
		return Receipt{}, false, errors.New("invalid receipt destination")
	}
	return Receipt{ID: NumberID(id), URL: want}, true, nil
}

// changesRequested reports whether the actor currently requests changes.
func changesRequested(x Transport, i *Item) (bool, error) {
	v, err := bitbucketGet(x, bitbucketPR(i), false)
	if err != nil {
		return false, err
	}
	m, _ := record(v)
	parts, ok := m["participants"].([]any)
	if !ok {
		return false, errors.New("invalid pull request participants")
	}
	for _, p := range parts {
		pm, _ := record(p)
		user, _ := record(pm["user"])
		if StringID(fmt.Sprint(user["uuid"])) == i.Snapshot.Actor.ID {
			return pm["state"] == "changes_requested", nil
		}
	}
	return false, nil
}

func (Bitbucket) Existing(x Transport, i *Item) ([]Receipt, error) {
	v, err := bitbucketGet(x, bitbucketPR(i)+"/comments?pagelen=50", true)
	if err != nil {
		return nil, err
	}
	comments, ok := v.([]any)
	if !ok || len(comments) > 100000 {
		return nil, errors.New("invalid paginated destination response")
	}
	found, err := matchReceipts(comments, func(v any) (bool, error) {
		_, ok, err := bitbucketComment(v, i)
		return ok, err
	}, func(v any) (Receipt, error) {
		r, _, err := bitbucketComment(v, i)
		return r, err
	})
	if err != nil || len(found) == 0 || i.Kind != KindPRReview || i.ReviewEvent != EventRequestChanges {
		return found, err
	}
	// A request-changes review is complete only when both its comment and the
	// actor's changes-requested state exist; a comment alone stays unmatched,
	// so an interrupted review remains uncertain and is never resent.
	requested, err := changesRequested(x, i)
	if err != nil || !requested {
		return nil, err
	}
	return found, nil
}

func (b Bitbucket) Publish(x Transport, i *Item) (Receipt, error) {
	payload := i.Payload
	out, err := x.Run(b.PublishCommand(i), &payload)
	if err != nil {
		return Receipt{}, err
	}
	v, err := decode(out)
	if err != nil {
		return Receipt{}, err
	}
	r, ok, err := bitbucketComment(v, i)
	if err != nil {
		return Receipt{}, err
	}
	if !ok {
		return Receipt{}, errors.New("invalid receipt actor or body")
	}
	if i.Kind == KindPRReview && i.ReviewEvent == EventRequestChanges {
		empty := "{}"
		res, err := x.Run(bitbucketArgv(bitbucketPR(i)+"/request-changes", "POST", false), &empty)
		if err != nil {
			return Receipt{}, err
		}
		pv, err := decode(res)
		pm, _ := record(pv)
		if err != nil || pm["state"] != "changes_requested" {
			return Receipt{}, errors.New("invalid request-changes receipt")
		}
	}
	return r, nil
}
