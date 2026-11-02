package hostruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	glBase = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	glHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	glPath = "group/sub/app"
)

// fakeGitLab answers `glab api` exactly as GitLab.com's REST v4 shapes them
// (recorded field names; values are fixtures).
type fakeGitLab struct {
	t          *testing.T
	head       string
	state      string
	actor      int
	diffReady  bool
	drafts     int
	notes      []map[string]any
	lose, drop bool
	calls      []string
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	return &fakeGitLab{t: t, head: glHead, state: "opened", actor: 7, diffReady: true, notes: []map[string]any{}}
}

func (g *fakeGitLab) run(argv []string, stdin *string) (string, error) {
	switch argv[0] {
	case "homonto":
		return `{"schema_version":2,"config_path":"/ws/homonto.toml","config_root":"/ws","repos":{"app":"/ws/app"}}`, nil
	case "git":
		return "git@gitlab.com:" + glPath + ".git\n", nil
	}
	if argv[0] != "glab" || argv[1] != "api" || argv[3] != "gitlab.com" {
		g.t.Fatalf("unexpected command %v", argv)
	}
	method := argv[5]
	endpoint := ""
	for _, a := range argv[6:] {
		if strings.HasPrefix(a, "projects/") || a == "user" {
			endpoint = a
		}
	}
	g.calls = append(g.calls, method+" "+endpoint)
	j := func(v any) (string, error) { b, _ := json.Marshal(v); return string(b), nil }
	switch {
	case endpoint == "projects/group%2Fsub%2Fapp":
		return j(map[string]any{"id": 55, "path_with_namespace": glPath, "web_url": "https://gitlab.com/" + glPath})
	case endpoint == "user":
		return j(map[string]any{"id": g.actor, "username": "author"})
	case endpoint == "projects/55/issues/1":
		return j(map[string]any{"id": 900, "iid": 1, "project_id": 55, "state": "opened", "title": "Bug", "description": nil,
			"updated_at": "2026-11-01T10:00:00.000Z", "discussion_locked": nil, "web_url": "https://gitlab.com/" + glPath + "/-/issues/1"})
	case endpoint == "projects/55/merge_requests/2":
		mr := map[string]any{"id": 901, "iid": 2, "project_id": 55, "target_project_id": 55, "source_project_id": 77, "state": g.state,
			"title": "Fix", "description": "d", "updated_at": "2026-11-01T10:00:00.000Z", "discussion_locked": false, "draft": false,
			"sha": g.head, "web_url": "https://gitlab.com/" + glPath + "/-/merge_requests/2", "diff_refs": nil}
		if g.diffReady {
			mr["diff_refs"] = map[string]any{"base_sha": glBase, "start_sha": glBase, "head_sha": g.head}
		}
		return j(mr)
	case strings.HasSuffix(endpoint, "/draft_notes") && method == "GET":
		list := make([]any, g.drafts)
		for n := range list {
			list[n] = map[string]any{"id": n + 1, "note": "pending"}
		}
		return j(list)
	case strings.Contains(endpoint, "/notes?") && method == "GET":
		if !slicesContains(argv, "--paginate") {
			g.t.Fatal("notes must be read with --paginate")
		}
		return j(g.notes)
	case method == "POST":
		if stdin == nil || !slicesContains(argv, "--input") {
			g.t.Fatal("POST without a stdin body")
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(*stdin), &body); err != nil {
			g.t.Fatal(err)
		}
		iid, kind := 1, "Issue"
		if strings.Contains(endpoint, "merge_requests") {
			iid, kind = 2, "MergeRequest"
		}
		text := body["body"]
		if strings.HasSuffix(endpoint, "/draft_notes/bulk_publish") {
			text = body["note"]
			if body["reviewer_state"] != "reviewed" && body["reviewer_state"] != "requested_changes" {
				g.t.Fatalf("reviewer_state = %q", body["reviewer_state"])
			}
		}
		note := map[string]any{"id": 1000 + len(g.notes), "body": text, "system": false, "noteable_iid": iid, "noteable_type": kind,
			"author": map[string]any{"id": g.actor, "username": "author"}}
		if !g.drop {
			g.notes = append(g.notes, note)
		}
		if g.lose || g.drop {
			return "", errors.New("process exited 1: connection reset")
		}
		if strings.HasSuffix(endpoint, "/bulk_publish") {
			return "", nil
		}
		return j(note)
	}
	g.t.Fatalf("unexpected endpoint %s %s", method, endpoint)
	return "", nil
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

type glCall struct {
	g    *fakeGitLab
	asks []string
}

func (c *glCall) SessionID() string { return "s1" }
func (c *glCall) Progress(string)   {}
func (c *glCall) Err() error        { return nil }
func (c *glCall) Ask(p string, patterns []string, _ map[string]any) error {
	c.asks = append(c.asks, p+" "+strings.Join(patterns, " "))
	return nil
}
func (c *glCall) Run(argv []string, stdin *string, _ time.Duration) (string, error) {
	return c.g.run(argv, stdin)
}

func glService(t *testing.T) (*Service, *fakeGitLab, *glCall, *fixture) {
	g := newFakeGitLab(t)
	f := &fixture{t: t, clock: 1_700_000_000_000}
	s, err := NewService(GitLab{}, "/ws/homonto.toml", func() int64 { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	return s, g, &glCall{g: g}, f
}

func glItem(kind, event string) string {
	if kind == KindIssueComment {
		return `{"kind":"issue_comment","url":"https://gitlab.com/` + glPath + `/-/issues/1","body":"Findings.","baseOID":"","headOID":"","reviewEvent":""}`
	}
	return fmt.Sprintf(`{"kind":%q,"url":"https://gitlab.com/%s/-/merge_requests/2","body":"Review.","baseOID":%q,"headOID":%q,"reviewEvent":%q}`, kind, glPath, glBase, glHead, event)
}

func glStage(t *testing.T, f *fixture, c *glCall, item string) view {
	t.Helper()
	out, err := f.s.Stage(c, f.args(`{"items":[`+item+`]}`))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	var v view
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func glPublish(t *testing.T, f *fixture, c *glCall, d view) (string, map[string]any) {
	t.Helper()
	out, err := f.s.Publish(c, f.args(`{"draftID":"`+d.DraftID+`"}`))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	var v struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	return v.Items[0]["status"].(string), v.Items[0]
}

func TestGitLabPublishesEachItemKind(t *testing.T) {
	for _, tc := range []struct{ kind, event, endpoint string }{
		{KindIssueComment, "", "POST projects/55/issues/1/notes"},
		{KindPRComment, "", "POST projects/55/merge_requests/2/notes"},
		{KindPRReview, EventComment, "POST projects/55/merge_requests/2/draft_notes/bulk_publish"},
		{KindPRReview, EventRequestChanges, "POST projects/55/merge_requests/2/draft_notes/bulk_publish"},
	} {
		t.Run(tc.kind+tc.event, func(t *testing.T) {
			_, g, c, f := glService(t)
			d := glStage(t, f, c, glItem(tc.kind, tc.event))
			f.s.Before("question", "s1", "call-1", f.args(marshal(d.Question, false)))
			f.approve(d)
			status, item := glPublish(t, f, c, d)
			if status != "published" || len(g.notes) != 1 {
				t.Fatalf("status %s notes %v item %v calls %v", status, g.notes, item, g.calls)
			}
			if !slicesContains(g.calls, tc.endpoint) {
				t.Fatalf("calls %v missing %s", g.calls, tc.endpoint)
			}
			url := item["receipt"].(map[string]any)["url"].(string)
			if !strings.HasPrefix(url, "https://gitlab.com/"+glPath+"/-/") || !strings.Contains(url, "#note_1000") {
				t.Fatalf("receipt url %s", url)
			}
			if !strings.Contains(c.asks[0], "homonto_gitlab_read") || !strings.HasPrefix(c.asks[1], "bash glab api --hostname gitlab.com --method POST") {
				t.Fatalf("permissions %v", c.asks)
			}
		})
	}
}

func TestGitLabStaleAndNotReadyMergeRequests(t *testing.T) {
	for name, mutate := range map[string]func(*fakeGitLab){
		"head moved": func(g *fakeGitLab) { g.head = strings.Repeat("c", 40) },
		"merged":     func(g *fakeGitLab) { g.state = "merged" },
		"actor":      func(g *fakeGitLab) { g.actor = 8 },
	} {
		t.Run(name, func(t *testing.T) {
			_, g, c, f := glService(t)
			d := glStage(t, f, c, glItem(KindPRComment, ""))
			f.approve(d)
			mutate(g)
			if status, _ := glPublish(t, f, c, d); status != "stale" || len(g.notes) != 0 {
				t.Fatalf("status %s, notes %v", status, g.notes)
			}
		})
	}
	t.Run("diff refs not ready", func(t *testing.T) {
		_, g, c, f := glService(t)
		g.diffReady = false
		_, err := f.s.Stage(c, f.args(`{"items":[`+glItem(KindPRComment, "")+`]}`))
		if err == nil || !strings.Contains(err.Error(), "diff is not ready") {
			t.Fatalf("stage with empty diff_refs = %v", err)
		}
	})
}

func TestGitLabReviewRefusesPendingDraftNotes(t *testing.T) {
	_, g, c, f := glService(t)
	d := glStage(t, f, c, glItem(KindPRReview, EventRequestChanges))
	f.approve(d)
	g.drafts = 2
	status, item := glPublish(t, f, c, d)
	if status != "blocked" || len(g.notes) != 0 || slicesContains(g.calls, "POST projects/55/merge_requests/2/draft_notes/bulk_publish") {
		t.Fatalf("status %s item %v calls %v", status, item, g.calls)
	}
}

func TestGitLabUncertainSend(t *testing.T) {
	t.Run("lost response reconciles", func(t *testing.T) {
		_, g, c, f := glService(t)
		d := glStage(t, f, c, glItem(KindIssueComment, ""))
		f.approve(d)
		g.lose = true
		if status, _ := glPublish(t, f, c, d); status != "published" || len(g.notes) != 1 {
			t.Fatalf("status %s notes %d", status, len(g.notes))
		}
	})
	t.Run("lost note is never resent", func(t *testing.T) {
		_, g, c, f := glService(t)
		d := glStage(t, f, c, glItem(KindPRReview, EventComment))
		f.approve(d)
		g.drop = true
		if status, _ := glPublish(t, f, c, d); status != "uncertain" {
			t.Fatalf("status %s", status)
		}
		g.drop = false
		posts := 0
		if status, _ := glPublish(t, f, c, d); status != "uncertain" {
			t.Fatalf("second publish status %s", status)
		}
		for _, call := range g.calls {
			if strings.HasPrefix(call, "POST") {
				posts++
			}
		}
		if posts != 1 {
			t.Fatalf("uncertain review resent: %d POSTs", posts)
		}
	})
}

func TestGitLabParsing(t *testing.T) {
	for _, bad := range []string{
		"https://gitlab.example.com/a/b/-/issues/1",
		"https://gitlab.com/a/-/issues/0",
		"https://gitlab.com/onlyone/-/issues/1",
		"https://gitlab.com/a/../b/-/issues/1",
		"https://gitlab.com/a/b/-/pipelines/1",
	} {
		if _, err := (GitLab{}).ParseTarget(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	tg, err := GitLab{}.ParseTarget("https://gitlab.com/g/s/p/-/merge_requests/12")
	if err != nil || tg.Repo != "g/s/p" || !tg.Change || tg.Number != 12 {
		t.Fatalf("parse = %+v, %v", tg, err)
	}
	for remote, want := range map[string]string{
		"git@gitlab.com:g/s/p.git":         "g/s/p",
		"https://gitlab.com/g/p.git":       "g/p",
		"ssh://git@gitlab.com/g/s/p.git\n": "g/s/p",
	} {
		if _, got, err := (GitLab{}).Origin(remote); err != nil || got != want {
			t.Errorf("origin %q = %q, %v", remote, got, err)
		}
	}
	if _, _, err := (GitLab{}).Origin("git@github.com:g/p.git"); err == nil {
		t.Error("accepted a non-GitLab origin")
	}
}
