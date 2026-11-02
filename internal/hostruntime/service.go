package hostruntime

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	ttl          = 15 * time.Minute
	maxSessions  = 4096
	maxDrafts    = 1024
	maxRequests  = 65536
	commandLimit = 8 * 1024 * 1024
	runTimeout   = 15 * time.Second
)

type option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type question struct {
	Header   string   `json:"header"`
	Question string   `json:"question"`
	Options  []option `json:"options"`
	Multiple bool     `json:"multiple"`
}

type questions struct {
	Questions []question `json:"questions"`
}

type session struct {
	deleted bool
	staging bool
	current string
}

type draft struct {
	id        string
	sessionID string
	expiresAt int64
	invalid   bool
	items     []*Item
	question  questions
	callID    string
	requestID string
	answered  bool
}

// Service is one plugin instance's draft state for one provider. It is not
// safe for concurrent use: the server runs it on a single logical thread.
type Service struct {
	provider   Provider
	configPath string
	now        func() int64 // milliseconds; the harness clock
	sessions   map[string]*session
	drafts     map[string]*draft
	order      []string
	requests   map[string]bool
	disposed   bool
	// cancelSession aborts a session's in-flight commands (session deletion).
	cancelSession func(sessionID string)
}

// NewService binds a provider to the selected config. now is the harness
// clock in milliseconds.
func NewService(p Provider, configPath string, now func() int64) (*Service, error) {
	if p == nil || now == nil || !pathOK(configPath) {
		return nil, errors.New("runner and bound absolute configPath are required")
	}
	return &Service{provider: p, configPath: configPath, now: now, sessions: map[string]*session{},
		drafts: map[string]*draft{}, requests: map[string]bool{}, cancelSession: func(string) {}}, nil
}

// pathOK mirrors the plugin's absolute normalized path check.
func pathOK(v string) bool {
	if !filepath.IsAbs(v) || strings.ContainsAny(v, "*?") {
		return false
	}
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	clean := path.Clean(v)
	return clean == v || (clean != "/" && clean+"/" == v)
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *Service) label() string { return s.provider.Label() }

func (s *Service) expire() {
	now := s.now()
	for _, id := range s.order {
		d := s.drafts[id]
		if now >= d.expiresAt {
			d.answered = true
			for _, i := range d.items {
				if i.Status == "pending" || i.Status == "approved" {
					i.Status = "expired"
				}
			}
		}
	}
}

func (s *Service) invalidate(d *draft) {
	d.invalid = true
	d.answered = true
	for _, i := range d.items {
		if i.Status == "pending" || i.Status == "approved" {
			i.Status = "invalidated"
		}
	}
}

func (s *Service) deleteDraft(id string) {
	delete(s.drafts, id)
	s.order = slices.DeleteFunc(s.order, func(v string) bool { return v == id })
}

func liveContextError(reason, detail string) error {
	return fmt.Errorf("[%s] %s; use a compatible live OpenCode session and retry the tool invocation", reason, detail)
}

func (s *Service) guard(c Call) (*session, error) {
	if c.Err() != nil {
		return nil, liveContextError("invocation_cancelled", "tool invocation was aborted; start a new invocation")
	}
	if s.disposed {
		return nil, fmt.Errorf("[plugin_disposed] %s plugin disposed; restart OpenCode in a compatible live session and stage a new draft", s.label())
	}
	s.expire()
	sess := s.sessions[c.SessionID()]
	if sess == nil {
		if len(s.sessions) >= maxSessions {
			return nil, fmt.Errorf("%s session capacity reached", s.label())
		}
		sess = &session{}
		s.sessions[c.SessionID()] = sess
	}
	if sess.deleted {
		return nil, fmt.Errorf("[session_deleted] %s session deleted; open a new live OpenCode session and stage a new draft", s.label())
	}
	return sess, nil
}

func (s *Service) live(c Call, d *draft) error {
	sess, err := s.guard(c)
	if err != nil {
		return err
	}
	if d != nil && (d.invalid || sess.current != d.id || s.now() >= d.expiresAt) {
		return errors.New("draft invalidated or expired")
	}
	return nil
}

// transport binds a Call to the core's command guard.
type transport struct {
	s *Service
	c Call
}

func (t transport) Run(argv []string, stdin *string) (string, error) {
	if _, err := t.s.guard(t.c); err != nil {
		return "", err
	}
	if stdin == nil {
		if len(argv) > 0 && argv[0] == t.s.provider.CLI() {
			t.c.Progress("Reading " + t.s.label())
		} else {
			t.c.Progress("Checking workspace")
		}
	}
	if _, err := t.s.guard(t.c); err != nil {
		return "", err
	}
	out, err := t.c.Run(argv, stdin, runTimeout)
	if _, gerr := t.s.guard(t.c); gerr != nil {
		return "", gerr
	}
	if err != nil {
		return "", err
	}
	if len(out) > commandLimit {
		return "", errors.New("command output exceeded limit")
	}
	return out, nil
}

// --- arguments ------------------------------------------------------------

func exactKeys(v any, keys ...string) (map[string]any, error) {
	m, ok := record(v)
	if !ok || len(m) != len(keys) {
		return nil, errors.New("invalid arguments")
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return nil, errors.New("invalid arguments")
		}
	}
	return m, nil
}

func blank(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }) == ""
}

func (s *Service) inputItems(args any) ([]InputItem, error) {
	m, err := exactKeys(args, "items")
	if err != nil {
		return nil, err
	}
	list, ok := m["items"].([]any)
	if !ok || len(list) == 0 || len(list) > 10 {
		return nil, errors.New("draft requires 1–10 items")
	}
	total := 0
	items := make([]InputItem, 0, len(list))
	for _, raw := range list {
		v, err := exactKeys(raw, "kind", "url", "body", "baseOID", "headOID", "reviewEvent")
		if err != nil {
			return nil, err
		}
		kind, _ := text(v["kind"])
		if kind != KindIssueComment && kind != KindPRComment && kind != KindPRReview {
			return nil, errors.New("unsupported operation")
		}
		url, _ := text(v["url"])
		if _, ok := text(v["url"]); !ok {
			return nil, fmt.Errorf("invalid %s URL", s.label())
		}
		t, err := s.provider.ParseTarget(url)
		if err != nil {
			return nil, err
		}
		body, ok := text(v["body"])
		if !ok || blank(body) || byteLen(body) > 8192 || strings.Contains(body, "\x00") {
			return nil, errors.New("body must contain 1–8192 UTF-8 bytes")
		}
		base, ok1 := text(v["baseOID"])
		head, ok2 := text(v["headOID"])
		event, ok3 := text(v["reviewEvent"])
		if !ok1 || !ok2 || !ok3 {
			return nil, errors.New("invalid review fields")
		}
		in := InputItem{Kind: kind, URL: url, Body: body, BaseOID: base, HeadOID: head, ReviewEvent: event}
		if err := s.provider.ValidateItem(in, t); err != nil {
			return nil, err
		}
		total += byteLen(body)
		items = append(items, in)
	}
	if total > 32768 {
		return nil, errors.New("batch bodies exceed 32 KiB")
	}
	return items, nil
}

// --- scope and snapshots --------------------------------------------------

type source struct{ alias, path string }

// orderedObject decodes a JSON object keeping key order.
func orderedObject(raw json.RawMessage) ([]source, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var out []source
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, false
		}
		key, _ := tok.(string)
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, false
		}
		p, _ := v.(string)
		out = append(out, source{key, p})
	}
	return out, true
}

func (s *Service) scope(x Transport) ([]Repo, error) {
	raw, err := x.Run([]string{"homonto", "workspace", "inspect", "--json", "--config", s.configPath}, nil)
	if err != nil {
		return nil, err
	}
	var v struct {
		SchemaVersion json.RawMessage `json:"schema_version"`
		ConfigPath    any             `json:"config_path"`
		ConfigRoot    any             `json:"config_root"`
		Repos         json.RawMessage `json:"repos"`
	}
	bad := errors.New("invalid workspace inspect contract")
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, bad
	}
	version := strings.TrimSpace(string(v.SchemaVersion))
	sources, ok := orderedObject(v.Repos)
	if (version != "0" && version != "1" && version != "2") || v.ConfigPath != s.configPath || v.ConfigRoot != filepath.Dir(s.configPath) || !ok {
		return nil, bad
	}
	if version != "2" {
		sources = append(sources, source{"config root", filepath.Dir(s.configPath)})
	}
	seen := map[string]bool{}
	for _, src := range sources {
		if !pathOK(src.path) || seen[src.path] {
			return nil, errors.New("invalid or empty declared source scope")
		}
		seen[src.path] = true
	}
	if len(sources) == 0 || len(sources) > 64 {
		return nil, errors.New("invalid or empty declared source scope")
	}
	var result []Repo
	for _, src := range sources {
		remote, err := x.Run([]string{"git", "-C", src.path, "remote", "get-url", "origin"}, nil)
		var host, name string
		if err == nil {
			host, name, err = s.provider.Origin(remote)
		}
		if err != nil {
			return nil, fmt.Errorf("source %s: origin validation failed", src.alias)
		}
		r, err := s.provider.Repository(x, host, name)
		if err != nil {
			return nil, fmt.Errorf("source %s: repository identity validation failed", src.alias)
		}
		if slices.ContainsFunc(result, func(o Repo) bool { return o.Host == r.Host && o.ID == r.ID }) {
			return nil, errors.New("ambiguous repository aliases")
		}
		result = append(result, r)
	}
	return result, nil
}

func (s *Service) snapshot(x Transport, t Target) (Snapshot, error) {
	allowed, err := s.scope(x)
	if err != nil {
		return Snapshot{}, err
	}
	if !slices.ContainsFunc(allowed, func(r Repo) bool { return r.Host == t.Host }) {
		return Snapshot{}, errors.New("target host is outside selected source scope")
	}
	r, err := s.provider.Repository(x, t.Host, t.Repo)
	if err != nil {
		return Snapshot{}, err
	}
	matches := 0
	for _, a := range allowed {
		if a.Host == r.Host && a.ID == r.ID {
			matches++
		}
	}
	if matches != 1 {
		return Snapshot{}, errors.New("target is outside selected source scope")
	}
	actor, err := s.provider.Actor(x, t.Host)
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := s.provider.Destination(x, t, r)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Repo, snap.Actor = r, actor
	return snap, nil
}

// --- views ----------------------------------------------------------------

type preview struct {
	ItemID      string `json:"itemID"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Repository  Repo   `json:"repository"`
	Actor       Actor  `json:"actor"`
	Body        string `json:"body"`
	BaseOID     string `json:"baseOID"`
	HeadOID     string `json:"headOID"`
	ReviewEvent string `json:"reviewEvent"`
}

func previewOf(i *Item) preview {
	return preview{i.ItemID, i.Kind, i.Target.URL, i.Snapshot.Repo, i.Snapshot.Actor, i.Body, i.BaseOID, i.HeadOID, i.ReviewEvent}
}

func marshal(v any, indent bool) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if indent {
		e.SetIndent("", "  ")
	}
	_ = e.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func (s *Service) view(d *draft) string {
	s.expire()
	type itemView struct {
		preview
		Status  string   `json:"status"`
		Receipt *Receipt `json:"receipt,omitempty"`
		Reason  string   `json:"reason,omitempty"`
	}
	items := make([]itemView, len(d.items))
	for n, i := range d.items {
		items[n] = itemView{previewOf(i), i.Status, i.Receipt, i.Reason}
	}
	return marshal(struct {
		DraftID   string     `json:"draftID"`
		ExpiresAt int64      `json:"expiresAt"`
		Items     []itemView `json:"items"`
		Question  questions  `json:"question"`
	}{d.id, d.expiresAt, items, d.question}, false)
}

func (s *Service) get(c Call, args any) (*draft, error) {
	if _, err := s.guard(c); err != nil {
		return nil, err
	}
	m, err := exactKeys(args, "draftID")
	if err != nil {
		return nil, err
	}
	id, ok := text(m["draftID"])
	if !ok {
		return nil, errors.New("draftID required")
	}
	d := s.drafts[id]
	if d == nil || d.sessionID != c.SessionID() {
		return nil, errors.New("unknown draft for this session")
	}
	return d, nil
}

// --- tools ----------------------------------------------------------------

// Status returns a draft's statuses, exact preview and question arguments.
func (s *Service) Status(c Call, args any) (string, error) {
	d, err := s.get(c, args)
	if err != nil {
		return "", err
	}
	return s.view(d), nil
}

// Stage builds an immutable draft and invalidates the session's earlier one.
func (s *Service) Stage(c Call, args any) (string, error) {
	sess, err := s.guard(c)
	if err != nil {
		return "", err
	}
	inputs, err := s.inputItems(args)
	if err != nil {
		return "", err
	}
	if sess.staging {
		return "", errors.New("draft staging already in progress")
	}
	for _, id := range s.order {
		d := s.drafts[id]
		if d.sessionID == c.SessionID() && slices.ContainsFunc(d.items, func(i *Item) bool { return i.Status == "publishing" || i.Status == "uncertain" }) {
			return "", errors.New("publication pending or uncertain; reconcile it before staging another draft")
		}
	}
	now := s.now()
	for _, id := range slices.Clone(s.order) {
		d := s.drafts[id]
		if now >= d.expiresAt && !slices.ContainsFunc(d.items, func(i *Item) bool { return i.Status == "publishing" }) {
			s.deleteDraft(id)
		}
	}
	if len(s.drafts) >= maxDrafts {
		return "", fmt.Errorf("%s draft capacity reached", s.label())
	}
	if sess.current != "" {
		if old := s.drafts[sess.current]; old != nil {
			s.invalidate(old)
		}
	}
	sess.current = ""
	sess.staging = true
	defer func() { sess.staging = false }()
	d := &draft{id: uuid(), sessionID: c.SessionID(), expiresAt: now + ttl.Milliseconds()}
	s.drafts[d.id] = d
	s.order = append(s.order, d.id)
	out, err := s.stageItems(c, sess, d, inputs)
	if err != nil {
		s.invalidate(d)
		s.deleteDraft(d.id)
		return "", err
	}
	return out, nil
}

func (s *Service) stageItems(c Call, sess *session, d *draft, inputs []InputItem) (string, error) {
	x := transport{s, c}
	for _, in := range inputs {
		c.Progress("Requesting " + s.label() + " read permission")
		if err := s.live(c, nil); err != nil {
			return "", err
		}
		if err := c.Ask("homonto_"+s.provider.ID()+"_read", []string{in.URL}, map[string]any{"url": in.URL}); err != nil {
			return "", err
		}
		if err := s.live(c, nil); err != nil {
			return "", err
		}
		t, err := s.provider.ParseTarget(in.URL)
		if err != nil {
			return "", err
		}
		snap, err := s.snapshot(x, t)
		if err != nil {
			return "", err
		}
		if in.BaseOID != snap.BaseOID || in.HeadOID != snap.HeadOID {
			return "", errors.New("reviewed base/head OIDs differ from remote")
		}
		if in.Kind == KindPRReview && (snap.State != "open" || snap.Merged) {
			return "", errors.New("formal review requires an open unmerged PR")
		}
		body := in.Body
		if in.Kind != KindIssueComment {
			body = fmt.Sprintf("%s\n\nReviewed base: %s\nReviewed head: %s", in.Body, in.BaseOID, in.HeadOID)
		}
		if byteLen(body) > 8192 {
			return "", errors.New("body with reviewed OIDs exceeds 8 KiB")
		}
		t.Repo = snap.Repo.Name
		t.URL = s.provider.CanonicalURL(t, snap.Repo.Name)
		in.Body = body
		item := &Item{InputItem: in, ItemID: uuid(), Target: t, Snapshot: snap, Status: "pending"}
		payload, err := s.provider.Payload(item)
		if err != nil {
			return "", err
		}
		item.Payload = payload
		d.items = append(d.items, item)
	}
	total := 0
	seen := map[string]bool{}
	for _, i := range d.items {
		total += byteLen(i.Body)
		key := i.Kind + "\x00" + i.Target.URL + "\x00" + i.Payload
		if seen[key] {
			return "", errors.New("duplicate items in batch")
		}
		seen[key] = true
	}
	if total > 32768 {
		return "", errors.New("batch bodies with OIDs exceed 32 KiB")
	}
	for n, i := range d.items {
		d.question.Questions = append(d.question.Questions, question{
			Header: fmt.Sprintf("%s %d", s.label(), n+1), Question: marshal(previewOf(i), true),
			Options: []option{
				{"Decline", "Do not publish this item."},
				{"Revise", "Request a new immutable draft."},
				{"Publish " + uuid(), "Approve this exact destination, actor, payload and reviewed commits."},
			},
		})
	}
	if err := s.live(c, nil); err != nil {
		return "", err
	}
	if s.now() >= d.expiresAt {
		return "", errors.New("draft expired while staging")
	}
	sess.current = d.id
	c.Progress(s.label() + " preview ready")
	if err := s.live(c, d); err != nil {
		return "", err
	}
	return s.view(d), nil
}

// reconcile adopts a single exact remote match. After an attempted send it
// re-checks liveness both before and after reading the destination.
func (s *Service) reconcile(c Call, x Transport, d *draft, i *Item, checkLive bool) {
	if checkLive && s.live(c, d) != nil {
		return
	}
	matches, err := s.provider.Existing(x, i)
	if err != nil || (checkLive && s.live(c, d) != nil) {
		return
	}
	if len(matches) == 1 {
		i.Receipt = &matches[0]
		i.Status = "published"
		i.Reason = ""
	}
}

// Publish sends only items approved through correlated native question events.
func (s *Service) Publish(c Call, args any) (string, error) {
	d, err := s.get(c, args)
	if err != nil {
		return "", err
	}
	x := transport{s, c}
	for _, i := range d.items {
		if i.Status == "uncertain" {
			s.reconcile(c, x, d, i, false)
		}
	}
	var selected []*Item
	for _, i := range d.items {
		if i.Status == "approved" {
			selected = append(selected, i)
		}
	}
	if len(selected) == 0 {
		return s.view(d), nil
	}
	if err := s.live(c, d); err != nil {
		return "", err
	}
	for _, i := range selected {
		i.Status = "publishing"
	}
	for _, i := range selected {
		attempted, err := s.publishOne(c, x, d, i)
		if err == nil {
			continue
		}
		if attempted {
			i.Status, i.Reason = "uncertain", "publication outcome uncertain; never resend this item"
			s.reconcile(c, x, d, i, true)
		} else {
			i.Status, i.Reason = "blocked", "publication preflight denied, invalidated or unavailable; create a new draft"
		}
	}
	return s.view(d), nil
}

func shellQuote(argv []string) string {
	parts := make([]string, len(argv))
	for n, a := range argv {
		plain := a != ""
		for _, c := range a {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_./:-", c)) {
				plain = false
				break
			}
		}
		if plain {
			parts[n] = a
		} else {
			parts[n] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

func (s *Service) publishOne(c Call, x Transport, d *draft, i *Item) (attempted bool, err error) {
	if err := s.live(c, d); err != nil {
		return false, err
	}
	pattern := shellQuote(s.provider.PublishCommand(i))
	c.Progress("Requesting " + s.label() + " publication permission")
	if err := s.live(c, d); err != nil {
		return false, err
	}
	if err := c.Ask("bash", []string{pattern}, map[string]any{"draftID": d.id, "itemID": i.ItemID, "url": i.Target.URL, "payload": i.Payload}); err != nil {
		return false, err
	}
	if err := s.live(c, d); err != nil {
		return false, err
	}
	matches, err := s.provider.Existing(x, i)
	if err != nil {
		return false, err
	}
	if err := s.live(c, d); err != nil {
		return false, err
	}
	fresh, err := s.snapshot(x, i.Target)
	if err != nil {
		return false, err
	}
	if err := s.live(c, d); err != nil {
		return false, err
	}
	switch {
	case !fresh.equal(i.Snapshot):
		i.Status, i.Reason = "stale", "destination snapshot or authenticated actor changed"
		return false, nil
	case len(matches) > 1:
		i.Status, i.Reason = "uncertain", "multiple matching remote publications"
		return false, nil
	case len(matches) == 1:
		i.Receipt, i.Status = &matches[0], "published"
		return false, nil
	}
	if err := s.provider.Preflight(x, i); err != nil {
		return false, err
	}
	if err := s.live(c, d); err != nil {
		return false, err
	}
	c.Progress("Ready to publish " + s.label() + " draft")
	if err := s.live(c, d); err != nil {
		return false, err
	}
	r, err := s.provider.Publish(x, i)
	if err != nil {
		return true, err
	}
	i.Receipt, i.Status = &r, "published"
	return true, nil
}

// --- harness events -------------------------------------------------------

// exactJSON compares two values as JSON documents: structs and decoded maps
// compare alike because both re-encode with sorted object keys.
func exactJSON(a, b any) bool {
	canonical := func(v any) string {
		out, err := decode(marshal(v, false))
		if err != nil {
			return "\x00invalid"
		}
		return marshal(out, false)
	}
	return canonical(a) == canonical(b)
}

func (s *Service) currentDraft(sessionID string) (*session, *draft) {
	sess := s.sessions[sessionID]
	if sess == nil || sess.current == "" {
		return sess, nil
	}
	return sess, s.drafts[sess.current]
}

func allPending(d *draft) bool {
	for _, i := range d.items {
		if i.Status != "pending" {
			return false
		}
	}
	return true
}

// Before correlates the native question tool call that shows a draft.
func (s *Service) Before(tool, sessionID, callID string, args any) {
	s.expire()
	if s.disposed || tool != "question" || callID == "" || len(callID) > 256 {
		return
	}
	sess, d := s.currentDraft(sessionID)
	if sess == nil || sess.deleted || d == nil || d.answered || d.callID != "" || !allPending(d) || !exactJSON(args, d.question) {
		return
	}
	d.callID = callID
}

func (s *Service) formFields(d *draft) any {
	type formOption struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	type field struct {
		Key         string       `json:"key"`
		Title       string       `json:"title"`
		Description string       `json:"description"`
		Type        string       `json:"type"`
		Custom      bool         `json:"custom"`
		Options     []formOption `json:"options"`
	}
	fields := make([]field, len(d.question.Questions))
	for n, q := range d.question.Questions {
		opts := make([]formOption, len(q.Options))
		for k, o := range q.Options {
			opts[k] = formOption{o.Label, o.Label, o.Description}
		}
		fields[n] = field{fmt.Sprintf("q%d", n), q.Header, q.Question, "string", true, opts}
	}
	return fields
}

// Event applies one harness bus event.
func (s *Service) Event(eventType string, props any) {
	s.expire()
	if eventType == "server.instance.disposed" {
		s.Dispose()
		return
	}
	p, ok := record(props)
	if s.disposed || !ok {
		return
	}
	switch eventType {
	case "session.deleted":
		raw := p["sessionID"]
		if info, ok := record(p["info"]); ok {
			raw = info["id"]
		}
		sessionID, ok := raw.(string)
		if !ok {
			return
		}
		if sess := s.sessions[sessionID]; sess != nil {
			sess.deleted = true
			s.cancelSession(sessionID)
			for _, id := range s.order {
				if d := s.drafts[id]; d.sessionID == sessionID {
					s.invalidate(d)
				}
			}
		} else if len(s.sessions) < maxSessions {
			s.sessions[sessionID] = &session{deleted: true}
		}
		return
	case "form.created":
		form, ok := record(p["form"])
		if !ok {
			return
		}
		sessionID, ok := text(form["sessionID"])
		if !ok {
			return
		}
		sess, d := s.currentDraft(sessionID)
		formID, idOK := text(form["id"])
		meta, metaOK := record(form["metadata"])
		var tool map[string]any
		toolOK := false
		if metaOK {
			tool, toolOK = record(meta["tool"])
		}
		fields, fieldsOK := form["fields"].([]any)
		if sess == nil || sess.deleted || d == nil || d.answered || d.callID == "" || d.requestID != "" || !allPending(d) ||
			!idOK || s.requests[formID] || len(s.requests) >= maxRequests || !metaOK || meta["kind"] != "question" ||
			!toolOK || tool["id"] != d.callID || !nonEmptyText(tool["messageID"]) || !fieldsOK || !exactJSON(fields, s.formFields(d)) {
			return
		}
		d.requestID = formID
		s.requests[formID] = true
		return
	case "form.replied", "form.cancelled":
		sessionID, ok := text(p["sessionID"])
		if !ok {
			return
		}
		sess, d := s.currentDraft(sessionID)
		if sess == nil || sess.deleted || d == nil || d.answered || d.requestID == "" || p["id"] != d.requestID {
			return
		}
		d.answered = true
		answer, ok := record(p["answer"])
		if eventType == "form.cancelled" || !ok || len(answer) != len(d.items) {
			for _, i := range d.items {
				i.Status = "declined"
			}
			return
		}
		for n, i := range d.items {
			a := answer[fmt.Sprintf("q%d", n)]
			switch a {
			case "Decline":
				i.Status = "declined"
			case "Revise":
				i.Status = "revision-requested"
			case d.question.Questions[n].Options[2].Label:
				i.Status = "approved"
			default:
				i.Status = "declined"
			}
		}
		return
	}
	sessionID, ok := text(p["sessionID"])
	if !ok {
		return
	}
	sess, d := s.currentDraft(sessionID)
	if sess == nil || sess.deleted || d == nil || d.answered || !allPending(d) {
		return
	}
	if eventType == "question.asked" {
		id, idOK := text(p["id"])
		tool, toolOK := record(p["tool"])
		if d.callID == "" || d.requestID != "" || !idOK || id == "" || len(id) > 256 || s.requests[id] || len(s.requests) >= maxRequests ||
			!toolOK || tool["callID"] != d.callID || !nonEmptyText(tool["messageID"]) || !exactJSON(p["questions"], d.question.Questions) {
			return
		}
		d.requestID = id
		s.requests[id] = true
		return
	}
	if d.requestID == "" || p["requestID"] != d.requestID {
		return
	}
	if eventType == "question.rejected" {
		d.answered = true
		for _, i := range d.items {
			i.Status = "declined"
		}
		return
	}
	if eventType != "question.replied" {
		return
	}
	d.answered = true
	answers, ok := p["answers"].([]any)
	valid := ok && len(answers) == len(d.items)
	choices := make([]string, len(d.items))
	for n := 0; valid && n < len(answers); n++ {
		a, ok := answers[n].([]any)
		if !ok || len(a) != 1 {
			valid = false
			break
		}
		label, ok := a[0].(string)
		valid = ok && slices.ContainsFunc(d.question.Questions[n].Options, func(o option) bool { return o.Label == label })
		choices[n] = label
	}
	for n, i := range d.items {
		switch {
		case !valid || choices[n] == "Decline":
			i.Status = "declined"
		case choices[n] == "Revise":
			i.Status = "revision-requested"
		default:
			i.Status = "approved"
		}
	}
}

func nonEmptyText(v any) bool { s, ok := v.(string); return ok && s != "" }

// Dispose invalidates every draft; later calls fail closed.
func (s *Service) Dispose() {
	if s.disposed {
		return
	}
	s.disposed = true
	for _, id := range s.order {
		s.invalidate(s.drafts[id])
	}
}

// SessionState is the per-session summary the shim mirrors for context injection.
type SessionState struct {
	SessionID string       `json:"sessionID"`
	Deleted   bool         `json:"deleted"`
	Drafts    []DraftState `json:"drafts"`
}

// DraftState is one draft's summary.
type DraftState struct {
	DraftID   string      `json:"draftID"`
	ExpiresAt int64       `json:"expiresAt"`
	Items     []ItemState `json:"items"`
}

// ItemState is one item's status.
type ItemState struct {
	ItemID string `json:"itemID"`
	Status string `json:"status"`
}

// States summarizes every known session, sorted by session ID.
func (s *Service) States() []SessionState {
	byID := map[string]*SessionState{}
	for id, sess := range s.sessions {
		byID[id] = &SessionState{SessionID: id, Deleted: sess.deleted, Drafts: []DraftState{}}
	}
	for _, id := range s.order {
		d := s.drafts[id]
		st := byID[d.sessionID]
		if st == nil {
			st = &SessionState{SessionID: d.sessionID, Drafts: []DraftState{}}
			byID[d.sessionID] = st
		}
		items := make([]ItemState, len(d.items))
		for n, i := range d.items {
			items[n] = ItemState{i.ItemID, i.Status}
		}
		st.Drafts = append(st.Drafts, DraftState{d.id, d.expiresAt, items})
	}
	out := make([]SessionState, 0, len(byID))
	for _, st := range byID {
		out = append(out, *st)
	}
	slices.SortFunc(out, func(a, b SessionState) int { return strings.Compare(a.SessionID, b.SessionID) })
	return out
}
