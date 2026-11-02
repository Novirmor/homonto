package hostruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeProvider is a host with one repository whose destination state the test
// controls. It proves the lifecycle is host-neutral: nothing here is GitHub.
type fakeProvider struct {
	head      string
	state     string
	actor     string
	posted    []string // payloads that reached the host
	lose      bool     // record the publication but fail the response
	drop      bool     // fail without recording it
	duplicate bool     // report two matching remote records
}

const fakeOID = "1111111111111111111111111111111111111111"

func (*fakeProvider) ID() string    { return "fake" }
func (*fakeProvider) Label() string { return "Fake" }
func (*fakeProvider) CLI() string   { return "fakecli" }

func (*fakeProvider) ParseTarget(raw string) (Target, error) {
	var n int64
	if _, err := fmt.Sscanf(raw, "fake://host.example/team/app/change/%d", &n); err == nil {
		return Target{Host: "host.example", Repo: "team/app", Number: n, Type: "change", Change: true, URL: raw}, nil
	}
	if _, err := fmt.Sscanf(raw, "fake://host.example/team/app/issue/%d", &n); err == nil {
		return Target{Host: "host.example", Repo: "team/app", Number: n, Type: "issue", URL: raw}, nil
	}
	return Target{}, errors.New("expected a fake URL")
}

func (*fakeProvider) ValidateItem(in InputItem, t Target) error {
	if in.Kind == KindIssueComment {
		return check(!t.Change && in.BaseOID == "" && in.HeadOID == "", "issue fields must be empty")
	}
	return check(t.Change && isOID(in.BaseOID) && isOID(in.HeadOID), "PR requires reviewed base/head OIDs")
}

func (*fakeProvider) Origin(remote string) (string, string, error) {
	if strings.TrimSpace(remote) != "fake@host.example:team/app" {
		return "", "", errors.New("unsupported origin URL")
	}
	return "host.example", "team/app", nil
}

func (f *fakeProvider) Repository(x Transport, host, name string) (Repo, error) {
	if _, err := x.Run([]string{"fakecli", "repo", name}, nil); err != nil {
		return Repo{}, err
	}
	return Repo{Host: host, ID: StringID("{repo-uuid}"), Name: "team/app"}, nil
}

func (f *fakeProvider) Actor(x Transport, host string) (Actor, error) {
	if _, err := x.Run([]string{"fakecli", "user"}, nil); err != nil {
		return Actor{}, err
	}
	return Actor{ID: StringID(f.actor), Login: f.actor}, nil
}

func (f *fakeProvider) Destination(x Transport, t Target, r Repo) (Snapshot, error) {
	if _, err := x.Run([]string{"fakecli", "view", t.URL}, nil); err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{ID: NumberID(t.Number), Number: t.Number, State: f.state, Title: "t", UpdatedAt: "2026-11-02T00:00:00Z"}
	if t.Change {
		s.BaseOID, s.HeadOID = fakeOID, f.head
	}
	return s, nil
}

func (*fakeProvider) CanonicalURL(t Target, repo string) string { return t.URL }

func (*fakeProvider) Payload(i *Item) (string, error) {
	return marshal(map[string]string{"body": i.Body}, false), nil
}

func (*fakeProvider) Preflight(Transport, *Item) error { return nil }

func (*fakeProvider) PublishCommand(i *Item) []string {
	return []string{"fakecli", "post", i.Target.URL}
}

func (f *fakeProvider) Publish(x Transport, i *Item) (Receipt, error) {
	payload := i.Payload
	if _, err := x.Run(f.PublishCommand(i), &payload); err != nil {
		return Receipt{}, err
	}
	if !f.drop {
		f.posted = append(f.posted, i.Payload)
	}
	if f.lose || f.drop {
		return Receipt{}, errors.New("connection reset")
	}
	return Receipt{ID: NumberID(int64(len(f.posted))), URL: fmt.Sprintf("%s#%d", i.Target.URL, len(f.posted))}, nil
}

func (f *fakeProvider) Existing(x Transport, i *Item) ([]Receipt, error) {
	if _, err := x.Run([]string{"fakecli", "list", i.Target.URL}, nil); err != nil {
		return nil, err
	}
	var out []Receipt
	for n, p := range f.posted {
		if p == i.Payload {
			out = append(out, Receipt{ID: NumberID(int64(n + 1)), URL: fmt.Sprintf("%s#%d", i.Target.URL, n+1)})
		}
	}
	if f.duplicate && len(out) == 1 {
		out = append(out, Receipt{ID: NumberID(99), URL: i.Target.URL + "#99"})
	}
	return out, nil
}

// fakeCall answers commands like `homonto workspace inspect` and `git remote`
// and records permission requests.
type fakeCall struct {
	session string
	asks    []string
	deny    bool
	aborted bool
}

func (c *fakeCall) SessionID() string { return c.session }
func (c *fakeCall) Progress(string)   {}
func (c *fakeCall) Err() error {
	if c.aborted {
		return errCancelled
	}
	return nil
}

func (c *fakeCall) Ask(permission string, _ []string, _ map[string]any) error {
	c.asks = append(c.asks, permission)
	if c.deny && permission == "bash" {
		return errors.New("denied")
	}
	return nil
}

func (c *fakeCall) Run(argv []string, _ *string, _ time.Duration) (string, error) {
	switch argv[0] {
	case "homonto":
		return `{"schema_version":2,"config_path":"/ws/homonto.toml","config_root":"/ws","repos":{"app":"/ws/app"}}`, nil
	case "git":
		return "fake@host.example:team/app\n", nil
	}
	return "{}", nil
}

type fixture struct {
	t     *testing.T
	p     *fakeProvider
	s     *Service
	clock int64
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, p: &fakeProvider{head: fakeOID, state: "open", actor: "me"}, clock: 1_700_000_000_000}
	s, err := NewService(f.p, "/ws/homonto.toml", func() int64 { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	return f
}

type view struct {
	DraftID string `json:"draftID"`
	Items   []struct {
		Status string `json:"status"`
	} `json:"items"`
	Question questions `json:"question"`
}

func (f *fixture) args(raw string) any {
	v, err := decode(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func (f *fixture) stage(c *fakeCall, items ...string) view {
	f.t.Helper()
	out, err := f.s.Stage(c, f.args(`{"items":[`+strings.Join(items, ",")+`]}`))
	if err != nil {
		f.t.Fatalf("stage: %v", err)
	}
	var v view
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		f.t.Fatal(err)
	}
	return v
}

func (f *fixture) publish(c *fakeCall, d view) []string {
	f.t.Helper()
	out, err := f.s.Publish(c, f.args(`{"draftID":"`+d.DraftID+`"}`))
	if err != nil {
		f.t.Fatalf("publish: %v", err)
	}
	var v view
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		f.t.Fatal(err)
	}
	statuses := make([]string, len(v.Items))
	for n, i := range v.Items {
		statuses[n] = i.Status
	}
	return statuses
}

// approve drives the native question flow: correlate the call, then answer.
func (f *fixture) approve(d view, choices ...string) {
	f.s.Before("question", "s1", "call-1", f.args(marshal(d.Question, false)))
	f.s.Event("question.asked", f.args(marshal(map[string]any{"id": "que-" + d.DraftID, "sessionID": "s1",
		"questions": d.Question.Questions, "tool": map[string]any{"callID": "call-1", "messageID": "m"}}, false)))
	answers := make([][]string, len(d.Question.Questions))
	for n := range answers {
		choice := "Publish"
		if n < len(choices) {
			choice = choices[n]
		}
		if choice == "Publish" {
			choice = d.Question.Questions[n].Options[2].Label
		}
		answers[n] = []string{choice}
	}
	f.s.Event("question.replied", f.args(marshal(map[string]any{"sessionID": "s1", "requestID": "que-" + d.DraftID, "answers": answers}, false)))
}

const (
	issueItem  = `{"kind":"issue_comment","url":"fake://host.example/team/app/issue/1","body":"Findings.","baseOID":"","headOID":"","reviewEvent":""}`
	changeItem = `{"kind":"pr_comment","url":"fake://host.example/team/app/change/2","body":"Summary.","baseOID":"` + fakeOID + `","headOID":"` + fakeOID + `","reviewEvent":""}`
)

func TestApprovalGatesPublication(t *testing.T) {
	f := newFixture(t)
	c := &fakeCall{session: "s1"}
	d := f.stage(c, issueItem)
	if got := f.publish(c, d); got[0] != "pending" || len(f.p.posted) != 0 {
		t.Fatalf("unapproved draft published: %v %v", got, f.p.posted)
	}
	f.approve(d)
	if got := f.publish(c, d); got[0] != "published" || len(f.p.posted) != 1 {
		t.Fatalf("approved draft: %v %v", got, f.p.posted)
	}
	if got := f.publish(c, d); got[0] != "published" || len(f.p.posted) != 1 {
		t.Fatalf("republish must be a no-op: %v %v", got, f.p.posted)
	}
	if strings.Join(c.asks, ",") != "homonto_fake_read,bash" {
		t.Fatalf("permissions = %v, want the provider's read permission then the publication command", c.asks)
	}
}

func TestDeclineReviseAndTamperedQuestionNeverPublish(t *testing.T) {
	f := newFixture(t)
	c := &fakeCall{session: "s1"}
	d := f.stage(c, issueItem, changeItem)
	f.approve(d, "Decline", "Revise")
	if got := f.publish(c, d); strings.Join(got, ",") != "declined,revision-requested" || len(f.p.posted) != 0 {
		t.Fatalf("statuses %v posted %v", got, f.p.posted)
	}

	d = f.stage(c, issueItem)
	tampered := d
	tampered.Question.Questions = append([]question(nil), d.Question.Questions...)
	tampered.Question.Questions[0].Question = "Approve?"
	f.approve(tampered)
	if got := f.publish(c, d); got[0] != "pending" {
		t.Fatalf("a question that differs from the staged one approved the draft: %v", got)
	}
}

func TestExpiryAndInvalidation(t *testing.T) {
	f := newFixture(t)
	c := &fakeCall{session: "s1"}
	d := f.stage(c, issueItem)
	f.approve(d)
	f.clock += ttl.Milliseconds()
	if got := f.publish(c, d); got[0] != "expired" || len(f.p.posted) != 0 {
		t.Fatalf("expired approval published: %v", got)
	}

	f.clock += 1
	first := f.stage(c, issueItem)
	f.approve(first)
	second := f.stage(c, changeItem)
	if got := f.publish(c, first); got[0] != "invalidated" || len(f.p.posted) != 0 {
		t.Fatalf("a newer draft must invalidate the earlier one: %v", got)
	}
	_ = second
}

func TestStaleDestinationBlocksPublication(t *testing.T) {
	for name, mutate := range map[string]func(*fakeProvider){
		"head moved":    func(p *fakeProvider) { p.head = strings.Repeat("2", 40) },
		"closed":        func(p *fakeProvider) { p.state = "closed" },
		"actor changed": func(p *fakeProvider) { p.actor = "someone-else" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			c := &fakeCall{session: "s1"}
			d := f.stage(c, changeItem)
			f.approve(d)
			mutate(f.p)
			if got := f.publish(c, d); got[0] != "stale" || len(f.p.posted) != 0 {
				t.Fatalf("stale destination published: %v", got)
			}
		})
	}
}

func TestUncertainSendIsReconciledNeverResent(t *testing.T) {
	t.Run("lost response is found", func(t *testing.T) {
		f := newFixture(t)
		c := &fakeCall{session: "s1"}
		d := f.stage(c, issueItem)
		f.approve(d)
		f.p.lose = true
		if got := f.publish(c, d); got[0] != "published" || len(f.p.posted) != 1 {
			t.Fatalf("lost response: %v %v", got, f.p.posted)
		}
	})
	t.Run("lost publication stays uncertain", func(t *testing.T) {
		f := newFixture(t)
		c := &fakeCall{session: "s1"}
		d := f.stage(c, issueItem)
		f.approve(d)
		f.p.drop = true
		if got := f.publish(c, d); got[0] != "uncertain" {
			t.Fatalf("lost publication: %v", got)
		}
		f.p.drop = false
		if got := f.publish(c, d); got[0] != "uncertain" || len(f.p.posted) != 0 {
			t.Fatalf("uncertain item resent: %v %v", got, f.p.posted)
		}
		if _, err := f.s.Stage(c, f.args(`{"items":[`+issueItem+`]}`)); err == nil || !strings.Contains(err.Error(), "uncertain") {
			t.Fatalf("staging over an uncertain item = %v, want refusal", err)
		}
	})
	t.Run("ambiguous match stays uncertain", func(t *testing.T) {
		f := newFixture(t)
		c := &fakeCall{session: "s1"}
		d := f.stage(c, issueItem)
		f.approve(d)
		f.p.posted = []string{`{"body":"Findings."}`}
		f.p.duplicate = true
		if got := f.publish(c, d); got[0] != "uncertain" {
			t.Fatalf("two remote matches: %v", got)
		}
	})
}

func TestDeniedPermissionAndSessionLifecycle(t *testing.T) {
	f := newFixture(t)
	c := &fakeCall{session: "s1"}
	d := f.stage(c, issueItem)
	f.approve(d)
	denied := &fakeCall{session: "s1", deny: true}
	if got := f.publish(denied, d); got[0] != "blocked" || len(f.p.posted) != 0 {
		t.Fatalf("denied permission: %v", got)
	}

	d = f.stage(c, issueItem)
	f.approve(d)
	cancelled := ""
	f.s.cancelSession = func(id string) { cancelled = id }
	f.s.Event("session.deleted", f.args(`{"info":{"id":"s1"}}`))
	if cancelled != "s1" {
		t.Fatal("session deletion must cancel the session's in-flight commands")
	}
	if _, err := f.s.Publish(c, f.args(`{"draftID":"`+d.DraftID+`"}`)); err == nil || !strings.Contains(err.Error(), "[session_deleted]") {
		t.Fatalf("publish after session deletion = %v", err)
	}
	if _, err := f.s.Status(&fakeCall{session: "s2"}, f.args(`{"draftID":"`+d.DraftID+`"}`)); err == nil || !strings.Contains(err.Error(), "unknown draft") {
		t.Fatalf("another session read the draft: %v", err)
	}
	f.s.Dispose()
	if _, err := f.s.Status(&fakeCall{session: "s2"}, f.args(`{"draftID":"x"}`)); err == nil || !strings.Contains(err.Error(), "[plugin_disposed] Fake plugin disposed") {
		t.Fatalf("disposed service = %v", err)
	}
	if _, err := f.s.Stage(&fakeCall{session: "s3", aborted: true}, f.args(`{"items":[`+issueItem+`]}`)); err == nil || !strings.Contains(err.Error(), "[invocation_cancelled]") {
		t.Fatalf("aborted call = %v", err)
	}
}
