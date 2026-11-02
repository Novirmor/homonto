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
	bbBase  = "dddddddddddddddddddddddddddddddddddddddd"
	bbHead  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	bbActor = "{a1b2c3}"
)

// fakeBitbucket answers `homonto host bitbucket api` with Bitbucket Cloud
// REST 2.0 shapes (field names from the published OpenAPI document).
type fakeBitbucket struct {
	t           *testing.T
	head        string
	state       string
	actor       string
	comments    []map[string]any
	requested   bool
	lose, drop  bool
	failRequest bool
	calls       []string
}

func (b *fakeBitbucket) run(argv []string, stdin *string) (string, error) {
	switch argv[0] {
	case "git":
		return "https://someone@bitbucket.org/ws/app.git\n", nil
	case "homonto":
		if argv[1] == "workspace" {
			return `{"schema_version":2,"config_path":"/ws/homonto.toml","config_root":"/ws","repos":{"app":"/ws/app"}}`, nil
		}
	}
	if strings.Join(argv[:4], " ") != "homonto host bitbucket api" {
		b.t.Fatalf("unexpected command %v", argv)
	}
	method, endpoint := argv[5], argv[len(argv)-1]
	if method == "POST" {
		endpoint = argv[len(argv)-3]
	}
	b.calls = append(b.calls, method+" "+endpoint)
	j := func(v any) (string, error) { out, _ := json.Marshal(v); return string(out), nil }
	pr := "repositories/ws/app/pullrequests/3"
	switch {
	case endpoint == "repositories/ws/app":
		return j(map[string]any{"uuid": "{repo}", "full_name": "ws/app", "links": map[string]any{"html": map[string]any{"href": "https://bitbucket.org/ws/app"}}})
	case endpoint == "user":
		return j(map[string]any{"uuid": b.actor, "nickname": "author", "account_id": "557058:x"})
	case endpoint == "repositories/fork/app/commit/"+b.head[:12]:
		return j(map[string]any{"hash": b.head})
	case endpoint == pr && method == "GET":
		state := map[bool]any{true: "changes_requested", false: nil}[b.requested]
		return j(map[string]any{"id": 3, "title": "Fix", "state": b.state, "draft": false, "updated_on": "2026-11-01T10:00:00.123456+00:00",
			"summary":      map[string]any{"raw": "desc"},
			"links":        map[string]any{"html": map[string]any{"href": "https://bitbucket.org/ws/app/pull-requests/3"}},
			"source":       map[string]any{"commit": map[string]any{"hash": b.head[:12]}, "repository": map[string]any{"full_name": "fork/app", "uuid": "{fork}"}},
			"destination":  map[string]any{"commit": map[string]any{"hash": bbBase}, "repository": map[string]any{"full_name": "ws/app", "uuid": "{repo}"}},
			"participants": []any{map[string]any{"user": map[string]any{"uuid": bbActor}, "state": state}}})
	case strings.HasPrefix(endpoint, pr+"/comments?") && method == "GET":
		return j(b.comments)
	case endpoint == pr+"/comments" && method == "POST":
		var body struct {
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
		}
		_ = json.Unmarshal([]byte(*stdin), &body)
		id := 500 + len(b.comments)
		c := map[string]any{"id": id, "deleted": false, "content": map[string]any{"raw": body.Content.Raw}, "user": map[string]any{"uuid": b.actor},
			"pullrequest": map[string]any{"id": 3}, "links": map[string]any{"html": map[string]any{"href": fmt.Sprintf("https://bitbucket.org/ws/app/pull-requests/3#comment-%d", id)}}}
		if !b.drop {
			b.comments = append(b.comments, c)
		}
		if b.lose || b.drop {
			return "", errors.New("process exited 1: bitbucket: POST: connection reset")
		}
		return j(c)
	case endpoint == pr+"/request-changes" && method == "POST":
		if b.failRequest {
			return "", errors.New("process exited 1: bitbucket: HTTP 500")
		}
		b.requested = true
		return j(map[string]any{"user": map[string]any{"uuid": b.actor}, "state": "changes_requested", "approved": false})
	}
	b.t.Fatalf("unexpected endpoint %s %s", method, endpoint)
	return "", nil
}

type bbCall struct{ b *fakeBitbucket }

func (c *bbCall) SessionID() string                                           { return "s1" }
func (c *bbCall) Progress(string)                                             {}
func (c *bbCall) Err() error                                                  { return nil }
func (c *bbCall) Ask(string, []string, map[string]any) error                  { return nil }
func (c *bbCall) Run(a []string, in *string, _ time.Duration) (string, error) { return c.b.run(a, in) }

func bbSetup(t *testing.T) (*fakeBitbucket, *bbCall, *fixture) {
	b := &fakeBitbucket{t: t, head: bbHead, state: "OPEN", actor: bbActor, comments: []map[string]any{}}
	f := &fixture{t: t, clock: 1_700_000_000_000}
	s, err := NewService(Bitbucket{}, "/ws/homonto.toml", func() int64 { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	return b, &bbCall{b}, f
}

func bbItem(kind, event string) string {
	return fmt.Sprintf(`{"kind":%q,"url":"https://bitbucket.org/ws/app/pull-requests/3","body":"Review.","baseOID":%q,"headOID":%q,"reviewEvent":%q}`, kind, bbBase, bbHead, event)
}

func bbRun(t *testing.T, f *fixture, c *bbCall, item string, before func()) string {
	t.Helper()
	out, err := f.s.Stage(c, f.args(`{"items":[`+item+`]}`))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	var d view
	_ = json.Unmarshal([]byte(out), &d)
	f.approve(d)
	if before != nil {
		before()
	}
	out, err = f.s.Publish(c, f.args(`{"draftID":"`+d.DraftID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var v view
	_ = json.Unmarshal([]byte(out), &v)
	return v.Items[0].Status
}

func TestBitbucketPublishesEachItemKind(t *testing.T) {
	for _, tc := range []struct{ kind, event string }{{KindPRComment, ""}, {KindPRReview, EventComment}, {KindPRReview, EventRequestChanges}} {
		t.Run(tc.kind+tc.event, func(t *testing.T) {
			b, c, f := bbSetup(t)
			if status := bbRun(t, f, c, bbItem(tc.kind, tc.event), nil); status != "published" || len(b.comments) != 1 {
				t.Fatalf("status %s comments %d calls %v", status, len(b.comments), b.calls)
			}
			if b.requested != (tc.event == EventRequestChanges) {
				t.Fatalf("request-changes sent = %v for %s", b.requested, tc.event)
			}
		})
	}
}

func TestBitbucketRejectsIssueItems(t *testing.T) {
	_, c, f := bbSetup(t)
	_, err := f.s.Stage(c, f.args(`{"items":[{"kind":"issue_comment","url":"https://bitbucket.org/ws/app/issues/1","body":"x","baseOID":"","headOID":"","reviewEvent":""}]}`))
	if err == nil || !strings.Contains(err.Error(), "no issue tracker") {
		t.Fatalf("issue item = %v", err)
	}
}

func TestBitbucketStaleness(t *testing.T) {
	for name, mutate := range map[string]func(*fakeBitbucket){
		"head moved": func(b *fakeBitbucket) { b.head = strings.Repeat("f", 40) },
		"declined":   func(b *fakeBitbucket) { b.state = "DECLINED" },
		"actor":      func(b *fakeBitbucket) { b.actor = "{other}" },
	} {
		t.Run(name, func(t *testing.T) {
			b, c, f := bbSetup(t)
			if status := bbRun(t, f, c, bbItem(KindPRComment, ""), func() { mutate(b) }); status != "stale" || len(b.comments) != 0 {
				t.Fatalf("status %s", status)
			}
		})
	}
}

func TestBitbucketUncertainSends(t *testing.T) {
	t.Run("lost response reconciles", func(t *testing.T) {
		b, c, f := bbSetup(t)
		if status := bbRun(t, f, c, bbItem(KindPRComment, ""), func() { b.lose = true }); status != "published" {
			t.Fatalf("status %s", status)
		}
	})
	t.Run("lost comment stays uncertain", func(t *testing.T) {
		b, c, f := bbSetup(t)
		if status := bbRun(t, f, c, bbItem(KindPRComment, ""), func() { b.drop = true }); status != "uncertain" {
			t.Fatalf("status %s", status)
		}
	})
	t.Run("comment without request-changes stays uncertain", func(t *testing.T) {
		b, c, f := bbSetup(t)
		if status := bbRun(t, f, c, bbItem(KindPRReview, EventRequestChanges), func() { b.failRequest = true }); status != "uncertain" || len(b.comments) != 1 || b.requested {
			t.Fatalf("status %s comments %d requested %v", status, len(b.comments), b.requested)
		}
	})
}

func TestBitbucketParsing(t *testing.T) {
	for _, bad := range []string{"https://bitbucket.example/ws/app/pull-requests/1", "https://bitbucket.org/ws/app/issues/1", "https://bitbucket.org/ws/../pull-requests/1"} {
		if _, err := (Bitbucket{}).ParseTarget(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	for _, remote := range []string{"git@bitbucket.org:ws/app.git", "https://user@bitbucket.org/ws/app.git", "https://bitbucket.org/ws/app"} {
		if _, got, err := (Bitbucket{}).Origin(remote); err != nil || got != "ws/app" {
			t.Errorf("origin %q = %q %v", remote, got, err)
		}
	}
	if _, _, err := (Bitbucket{}).Origin("https://user:pw@bitbucket.org/ws/app.git"); err == nil {
		t.Error("accepted an origin carrying a password")
	}
}
