package swarm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/workspace"
	"github.com/Novirmor/homonto/internal/worktask"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := workspace.Git(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// fixture is a schema 2 workspace with one changed repo (web), a check that
// fails when the candidate's "verdict" file says "broken", and an optional
// preview.
func fixture(t *testing.T, extra string) (*Store, *fakeDocker) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "web")
	put(t, filepath.Join(dir, "verdict"), "ok\n")
	put(t, filepath.Join(dir, "check.sh"), "set -eu\n! grep -q broken verdict\n")
	put(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "commit.gpgsign", "false")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	text := "schema_version=2\n[repos]\nweb='../web'\n[workflow]\nroot='../records'\n[worktrees]\ndir='../allocated'\n" + extra
	for _, engine := range []string{"onto", "no"} {
		text += fmt.Sprintf("[engines.%s]\nsource='builtin:%s'\nscope='project'\n", engine, engine)
	}
	for _, agent := range []string{"homonto", "onto-explorer", "onto-implementer", "onto-reviewer", "onto-skeptic", "no-implementer", "no-reviewer"} {
		text += fmt.Sprintf("[subagents.%s.opencode]\nmodel='test/model'\n", agent)
	}
	path := filepath.Join(root, "control", "homonto.toml")
	put(t, path, text)
	tasks, err := worktask.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d := newFakeDocker()
	s := &Store{Tasks: tasks, Docker: d, Probe: func(context.Context, string) error { return nil }, Spawn: func([]string) error { return nil }}
	return s, d
}

const check = "[integration.web]\nrepos=['web']\nrepo='web'\ncwd='.'\ncommand=['sh','check.sh']\n"

func specs(names ...string) []CandidateSpec {
	var out []CandidateSpec
	for _, n := range names {
		out = append(out, CandidateSpec{Name: n, Agent: "no-implementer", Models: map[string]string{"opencode": "test/" + n}})
	}
	return out
}

func brief(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	put(t, path, text)
	return path
}

func create(t *testing.T, s *Store, names ...string) *Swarm {
	t.Helper()
	sw, err := s.Create(context.Background(), "demo", "no", []string{"web"}, specs(names...), brief(t, "Build the page.\n"))
	if err != nil {
		t.Fatal(err)
	}
	return sw
}

// commit writes the candidate's verdict and commits it in its worktree.
func commit(t *testing.T, s *Store, candidate, verdict string) string {
	t.Helper()
	tk, err := s.Tasks.Load(context.Background(), TaskName("demo", candidate))
	if err != nil {
		t.Fatal(err)
	}
	dir := tk.Checkouts["web"].Path
	put(t, filepath.Join(dir, "verdict"), verdict+"\n")
	git(t, dir, "commit", "-am", "candidate "+candidate)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestCreateAllocatesOneWorktreePerCandidateFromOneBase(t *testing.T) {
	s, _ := fixture(t, check)
	sw := create(t, s, "a", "b", "c")
	base := sw.Bases["web"]
	if base == "" || len(sw.Candidates) != 3 {
		t.Fatalf("swarm = %+v", sw)
	}
	paths := map[string]bool{}
	for _, c := range sw.Candidates {
		tk, err := s.Tasks.Load(context.Background(), c.Task)
		if err != nil {
			t.Fatal(err)
		}
		co := tk.Checkouts["web"]
		if co.Base != base || co.Isolation != "worktree" || co.Path == s.Tasks.Layout.Repos["web"] || paths[co.Path] {
			t.Fatalf("candidate %s checkout = %+v", c.Name, co)
		}
		paths[co.Path] = true
		if got := git(t, co.Path, "rev-parse", "HEAD"); got != base {
			t.Fatalf("candidate %s HEAD %s, base %s", c.Name, got, base)
		}
		if c.Models["opencode"] != "test/"+c.Name {
			t.Fatalf("models = %v", c.Models)
		}
	}
	data, err := os.ReadFile(sw.Brief.Path)
	if err != nil || string(data) != "Build the page.\n" {
		t.Fatalf("brief = %q, %v", data, err)
	}
	// Repeating creation resumes; changing the request is refused.
	again, err := s.Create(context.Background(), "demo", "no", []string{"web"}, specs("a", "b", "c"), brief(t, "Build the page.\n"))
	if err != nil || again.Bases["web"] != base {
		t.Fatalf("resume = %+v, %v", again, err)
	}
	if _, err := s.Create(context.Background(), "demo", "no", []string{"web"}, specs("a", "b", "c"), brief(t, "Another brief.\n")); err == nil {
		t.Fatal("a different brief resumed the swarm")
	}
	changedModels := specs("a", "b", "c")
	changedModels[0].Models["opencode"] = "test/changed"
	if _, err := s.Create(context.Background(), "demo", "no", []string{"web"}, changedModels, brief(t, "Build the page.\n")); err == nil {
		t.Fatal("a changed route resumed the swarm")
	}
}

func TestCreateRefusesUnboundedOrLifecycleSwarms(t *testing.T) {
	s, _ := fixture(t, check)
	ctx := context.Background()
	b := brief(t, "x")
	for _, tc := range []struct {
		engine string
		specs  []CandidateSpec
		want   string
	}{
		{"no", specs("a"), "2 or 3 candidates"},
		{"no", specs("a", "b", "c", "d"), "2 or 3 candidates"},
		{"no", specs("a", "a"), "unique"},
		{"no", specs("a-b", "c"), "unique lowercase"},
		{"onto", specs("a", "b"), "native lifecycle"},
	} {
		_, err := s.Create(ctx, "demo", tc.engine, []string{"web"}, tc.specs, b)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %d candidates: err = %v, want %q", tc.engine, len(tc.specs), err, tc.want)
		}
	}
	if _, err := s.Create(ctx, "demo", "no", []string{"web"}, []CandidateSpec{{Name: "a", Agent: "x"}, {Name: "b", Agent: "y", Models: map[string]string{"opencode": "m"}}}, b); err == nil || !strings.Contains(err.Error(), "no routed model") {
		t.Errorf("unrouted agent: %v", err)
	}
}

func TestCompareRefusesMismatchedPinnedBases(t *testing.T) {
	s, _ := fixture(t, check)
	sw := create(t, s, "a", "b")
	sw.Bases["web"] = "different"
	if err := s.save(sw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compare(context.Background(), "demo"); err == nil || !strings.Contains(err.Error(), "pinned swarm bases") {
		t.Fatalf("comparison accepted mismatched bases: %v", err)
	}
}

func TestCompareSelectsOnlyAVerifiedCandidate(t *testing.T) {
	s, _ := fixture(t, check)
	create(t, s, "a", "b")
	ctx := context.Background()
	cmp, err := s.Compare(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmp.Candidates {
		if c.Verified || !slices.ContainsFunc(c.Unverified, func(r string) bool { return strings.Contains(r, "no commits") }) {
			t.Fatalf("an unchanged candidate looked verified: %+v", c)
		}
	}
	commit(t, s, "a", "good")
	commit(t, s, "b", "broken")
	runs, err := s.Check(ctx, "demo", "", time.Minute, io.Discard)
	if err != nil || len(runs) != 2 {
		t.Fatalf("check = %+v, %v", runs, err)
	}
	cmp, err = s.Compare(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]CandidateView{}
	for _, c := range cmp.Candidates {
		byName[c.Name] = c
	}
	if !byName["a"].Verified || byName["a"].Diff["web"].Commits != 1 || byName["a"].Diff["web"].Files != 1 {
		t.Fatalf("a = %+v", byName["a"])
	}
	if byName["b"].Verified || byName["b"].Checks["web"].Status != "failed" {
		t.Fatalf("b = %+v", byName["b"])
	}
	if _, err := s.Select(ctx, "demo", "b", false); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("selected a failing candidate: %v", err)
	}
	if _, err := s.Select(ctx, "demo", "a", true); err == nil {
		t.Fatal("--fallback-verified overrode a configured check")
	}
	// New work after the check makes the evidence stale.
	tk, _ := s.Tasks.Load(ctx, TaskName("demo", "a"))
	put(t, filepath.Join(tk.Checkouts["web"].Path, "verdict"), "dirty\n")
	if _, err := s.Select(ctx, "demo", "a", false); err == nil {
		t.Fatal("selected a candidate with uncommitted work")
	}
	git(t, tk.Checkouts["web"].Path, "checkout", "verdict")
	sw, err := s.Select(ctx, "demo", "a", false)
	if err != nil || sw.Selection.Candidate != "a" || sw.Selection.Revisions["b"]["web"] == "" {
		t.Fatalf("select = %+v, %v", sw, err)
	}
	path, err := s.WriteHTML(mustCompare(t, s))
	if err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(path)
	for _, want := range []string{"Swarm demo", "test/a", "not verified", "selected: <strong>a</strong>"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("compare.html lacks %q", want)
		}
	}
}

func mustCompare(t *testing.T, s *Store) *Comparison {
	t.Helper()
	cmp, err := s.Compare(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	return cmp
}

func TestFallbackSelectionNeedsAttestation(t *testing.T) {
	s, _ := fixture(t, "")
	create(t, s, "a", "b")
	commit(t, s, "a", "good")
	ctx := context.Background()
	if _, err := s.Select(ctx, "demo", "a", false); err == nil || !strings.Contains(err.Error(), "--fallback-verified") {
		t.Fatalf("selected without any verification: %v", err)
	}
	sw, err := s.Select(ctx, "demo", "a", true)
	if err != nil || !sw.Selection.Fallback {
		t.Fatalf("select = %+v, %v", sw, err)
	}
}

func TestCleanupDeletesOnlyTheRejectedWorkThatWasShown(t *testing.T) {
	s, _ := fixture(t, check)
	sw := create(t, s, "a", "b", "c")
	ctx := context.Background()
	if _, err := s.Cleanup(ctx, "demo", true, false, false); err == nil {
		t.Fatal("cleanup without a selection")
	}
	commit(t, s, "a", "good")
	commit(t, s, "b", "good-too")
	commit(t, s, "c", "broken")
	if _, err := s.Check(ctx, "demo", "", time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Select(ctx, "demo", "a", false); err != nil {
		t.Fatal(err)
	}
	source := s.Tasks.Layout.Repos["web"]
	// c moves after selection: its new commit was never shown, so it is kept.
	commit(t, s, "c", "broken-again")
	// Deliver the winner the normal way: merge its branch into main.
	git(t, source, "merge", "--ff-only", "work-demo--a/web")
	report, err := s.Cleanup(ctx, "demo", true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Removed, []string{"b", "c", "a"}) {
		t.Fatalf("removed = %v", report.Removed)
	}
	if !slices.Equal(report.Deleted, []string{"web:work-demo--b/web"}) || !slices.Equal(report.Retained, []string{"web:work-demo--c/web"}) {
		t.Fatalf("report = %+v", report)
	}
	branches := git(t, source, "branch", "--format=%(refname:short)")
	if strings.Contains(branches, "work-demo--a/") || strings.Contains(branches, "work-demo--b/") || !strings.Contains(branches, "work-demo--c/web") {
		t.Fatalf("branches = %q", branches)
	}
	for _, c := range sw.Candidates {
		if _, err := os.Stat(filepath.Join(s.Tasks.Layout.WorktreesDir, "web", "workspace-"+c.Task)); !os.IsNotExist(err) {
			t.Fatalf("candidate %s worktree remains: %v", c.Name, err)
		}
	}
	// Idempotent once cleaned.
	if _, err := s.Cleanup(ctx, "demo", true, false, false); err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
}

func TestAbandonKeepsEveryBranch(t *testing.T) {
	s, _ := fixture(t, check)
	create(t, s, "a", "b")
	commit(t, s, "a", "good")
	report, err := s.Cleanup(context.Background(), "demo", true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deleted) != 0 || !slices.Equal(report.Retained, []string{"web:work-demo--a/web", "web:work-demo--b/web"}) {
		t.Fatalf("report = %+v", report)
	}
}

func TestCleanupKeepRejectedPreservesIntegratedBranch(t *testing.T) {
	s, _ := fixture(t, "")
	create(t, s, "a", "b")
	commit(t, s, "a", "good")
	if _, err := s.Select(context.Background(), "demo", "a", true); err != nil {
		t.Fatal(err)
	}
	report, err := s.Cleanup(context.Background(), "demo", true, false, true)
	if err != nil || !slices.Contains(report.Retained, "web:work-demo--b/web") {
		t.Fatalf("cleanup = %+v, %v", report, err)
	}
}

// fakeDocker records containers in memory.
type fakeDocker struct {
	mu          sync.Mutex
	unavailable bool
	containers  map[string]Container
	images      map[string]bool
	runs        []RunSpec
	failRun     bool
	block       chan struct{} // when set, Run waits for it or ctx
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{containers: map[string]Container{}, images: map[string]bool{}}
}

func (f *fakeDocker) Available(context.Context) error {
	if f.unavailable {
		return errors.New("daemon down")
	}
	return nil
}

func (f *fakeDocker) Build(_ context.Context, contextDir, dockerfile, tag string, _ map[string]string, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := os.Stat(dockerfile); err != nil {
		return err
	}
	f.images[tag] = true
	return nil
}

func (f *fakeDocker) Run(ctx context.Context, spec RunSpec) error {
	f.mu.Lock()
	f.runs = append(f.runs, spec)
	f.containers[spec.Name] = Container{Name: spec.Name, Candidate: spec.Labels[LabelCandidate], Preview: spec.Labels[LabelPreview], Generation: spec.Labels[LabelGeneration], Deadline: spec.Labels[LabelDeadline], Running: true}
	block, fail := f.block, f.failRun
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail {
		return errors.New("exit 1")
	}
	return nil
}

func (f *fakeDocker) HostPort(context.Context, string, int) (int, error) { return 18080, nil }

func (f *fakeDocker) List(_ context.Context, label, value string) ([]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Container
	for _, c := range f.containers {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Container) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeDocker) Logs(context.Context, string, int) string { return "" }

func (f *fakeDocker) Remove(_ context.Context, names ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return errors.New("daemon down")
	}
	for _, n := range names {
		delete(f.containers, n)
	}
	return nil
}

func (f *fakeDocker) RemoveImage(_ context.Context, tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.images, tag)
	return nil
}

func (f *fakeDocker) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.containers)
}

const preview = "[preview.site]\nrepo='web'\ndockerfile='Dockerfile'\nport=8080\ncpus=0.5\nmemory='256m'\npids=64\nttl='10m'\nstartup='1m'\n"

func TestPreviewsRunUnderBudgetsAndExpire(t *testing.T) {
	s, d := fixture(t, check+preview)
	create(t, s, "a", "b", "c")
	ctx := context.Background()
	var spawned []string
	s.Spawn = func(args []string) error { spawned = args; return nil }
	started, err := s.StartPreviews(ctx, "demo", nil, io.Discard)
	if err != nil || len(started) != 3 {
		t.Fatalf("start = %+v, %v", started, err)
	}
	for _, p := range started {
		if p.State != PreviewRunning || p.URL != "http://127.0.0.1:18080/" || !d.images[p.Image] {
			t.Fatalf("preview = %+v", p)
		}
	}
	for _, spec := range d.runs {
		if spec.Budget.CPUs != 0.5 || spec.Budget.MemoryBytes != 256<<20 || spec.Budget.Pids != 64 || spec.Port != 8080 || spec.Labels[LabelSwarm] == "" {
			t.Fatalf("run spec = %+v", spec)
		}
	}
	if !slices.Contains(spawned, "reap") || !slices.Contains(spawned, "--until") {
		t.Fatalf("watchdog args = %v", spawned)
	}
	cmp := mustCompare(t, s)
	if len(cmp.Candidates[0].Previews) != 1 || cmp.Candidates[0].Previews[0].State != PreviewRunning {
		t.Fatalf("compare previews = %+v", cmp.Candidates[0].Previews)
	}
	// Past the deadline, any swarm command tears the previews down.
	s.Now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	cmp = mustCompare(t, s)
	if d.count() != 0 || len(d.images) != 0 {
		t.Fatalf("expired previews still running: %d containers, %d images", d.count(), len(d.images))
	}
	for _, c := range cmp.Candidates {
		if c.Previews[0].State != PreviewExpired || c.Previews[0].URL != "" {
			t.Fatalf("preview after deadline = %+v", c.Previews[0])
		}
	}
}

func TestPreviewWatchdogArmedForEachDeadlineBeforeRun(t *testing.T) {
	s, d := fixture(t, check+preview)
	create(t, s, "a", "b")
	var deadlines []time.Time
	s.Spawn = func(args []string) error {
		at, err := time.Parse(time.RFC3339Nano, args[len(args)-1])
		if err != nil {
			return err
		}
		if d.count() != len(deadlines) {
			t.Fatal("container created before its watchdog")
		}
		deadlines = append(deadlines, at)
		return nil
	}
	started, err := s.StartPreviews(context.Background(), "demo", nil, nil)
	if err != nil || len(deadlines) != 2 {
		t.Fatalf("start = %+v, deadlines %v, error %v", started, deadlines, err)
	}
	for i := range started {
		if !deadlines[i].Equal(started[i].Deadline) {
			t.Fatalf("watchdog deadline %v != preview deadline %v", deadlines[i], started[i].Deadline)
		}
	}
}

func TestPreviewDoesNotStartWithoutWatchdog(t *testing.T) {
	s, d := fixture(t, check+preview)
	create(t, s, "a", "b")
	s.Spawn = func([]string) error { return errors.New("cannot spawn") }
	started, err := s.StartPreviews(context.Background(), "demo", []string{"a"}, nil)
	if err == nil || len(started) != 1 || started[0].State != PreviewFailed || d.count() != 0 || len(d.runs) != 0 {
		t.Fatalf("start = %+v, error %v, runs %d", started, err, len(d.runs))
	}
}

func TestSlowLaterPreviewDoesNotHoldEarlierDeadline(t *testing.T) {
	s, d := fixture(t, check+"[preview.site]\nrepo='web'\nimage='nginx:1'\nport=80\nttl='250ms'\nstartup='10s'\n")
	create(t, s, "a", "b")
	count := 0
	s.Spawn = func([]string) error {
		count++
		if count == 2 {
			d.block = make(chan struct{})
		}
		return nil
	}
	start := time.Now()
	started, err := s.StartPreviews(context.Background(), "demo", nil, nil)
	if err == nil || len(started) != 2 || started[0].State != PreviewRunning || started[1].State != PreviewFailed {
		t.Fatalf("start = %+v, error %v", started, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("later startup blocked the earlier preview deadline")
	}
	if err := s.Reap(context.Background(), "demo", time.Time{}); err != nil || d.count() != 0 {
		t.Fatalf("reap error %v, containers %d", err, d.count())
	}
}

func TestInterruptedPreviewStartIsTornDown(t *testing.T) {
	s, d := fixture(t, check+preview)
	create(t, s, "a", "b")
	d.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.StartPreviews(ctx, "demo", []string{"a"}, io.Discard)
		done <- err
	}()
	for d.count() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("interrupted start reported success")
	}
	if d.count() != 0 || len(d.images) != 0 {
		t.Fatalf("interrupted start left %d containers, %d images", d.count(), len(d.images))
	}
	sw, err := s.read("demo")
	if err != nil || sw.Previews["a/site"].State != PreviewInterrupted {
		t.Fatalf("record = %+v, %v", sw.Previews, err)
	}
}

func TestReconcileRemovesWhatAKilledStartLeft(t *testing.T) {
	s, d := fixture(t, check+preview)
	sw := create(t, s, "a", "b")
	// A start killed after recording intent and starting the container, plus a
	// container with this swarm's label that the record never learned about.
	name := containerName(sw, "a", "site")
	sw.Previews["a/site"] = PreviewState{Candidate: "a", Preview: "site", State: PreviewStarting, Generation: 1, Container: name, Deadline: time.Now().Add(time.Hour)}
	if err := s.save(sw); err != nil {
		t.Fatal(err)
	}
	d.containers[name] = Container{Name: name, Candidate: "a", Generation: "1", Running: true}
	d.containers["orphan"] = Container{Name: "orphan", Candidate: "b", Generation: "7", Running: true}
	cmp := mustCompare(t, s)
	if d.count() != 0 {
		t.Fatalf("containers left: %v", d.containers)
	}
	if cmp.Candidates[0].Previews[0].State != PreviewInterrupted {
		t.Fatalf("preview = %+v", cmp.Candidates[0].Previews[0])
	}
}

func TestFailedPreviewIsTornDown(t *testing.T) {
	s, d := fixture(t, check+"[preview.site]\nrepo='web'\nimage='nginx:1'\nport=80\nstartup='1s'\n")
	create(t, s, "a", "b")
	s.Probe = func(context.Context, string) error { return errors.New("connection refused") }
	started, err := s.StartPreviews(context.Background(), "demo", []string{"b"}, io.Discard)
	if err == nil || len(started) != 1 || started[0].State != PreviewFailed || !strings.Contains(started[0].Error, "startup budget") {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if d.count() != 0 {
		t.Fatal("a preview that never served was left running")
	}
	if d.runs[0].Mount == "" || d.runs[0].Image != "nginx:1" {
		t.Fatalf("image mode spec = %+v", d.runs[0])
	}
}

func TestCleanupStopsPreviewsFirstAndRefusesWithoutDocker(t *testing.T) {
	s, d := fixture(t, check+preview)
	create(t, s, "a", "b")
	commit(t, s, "a", "good")
	ctx := context.Background()
	if _, err := s.StartPreviews(ctx, "demo", nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	d.unavailable = true
	if _, err := s.Cleanup(ctx, "demo", true, true, false); err == nil || !strings.Contains(err.Error(), "torn down") {
		t.Fatalf("cleanup without Docker: %v", err)
	}
	tk, _ := s.Tasks.Load(ctx, TaskName("demo", "a"))
	if _, err := os.Stat(tk.Checkouts["web"].Path); err != nil {
		t.Fatalf("worktree removed while its preview may still run: %v", err)
	}
	d.unavailable = false
	report, err := s.Cleanup(ctx, "demo", true, true, false)
	if err != nil || len(report.Previews) != 2 || d.count() != 0 {
		t.Fatalf("cleanup = %+v, %v, %d containers", report, err, d.count())
	}
}

func TestRunArgsCarryEveryBudget(t *testing.T) {
	args := strings.Join(RunArgs(RunSpec{Name: "n", Image: "img", Command: []string{"serve", "--port", "80"}, Port: 80, Mount: "/src", Labels: map[string]string{LabelSwarm: "id"},
		Budget: cfgBudget()}), " ")
	for _, want := range []string{"--cpus 1.5", "--memory 134217728 --memory-swap 134217728", "--pids-limit 32", "--publish 127.0.0.1::80", "--label io.homonto.swarm=id", "type=bind,source=/src,target=/workspace,readonly", "--entrypoint serve -- img --port 80", "no-new-privileges"} {
		if !strings.Contains(args, want) {
			t.Errorf("docker run args lack %q: %s", want, args)
		}
	}
}

func cfgBudget() config.PreviewBudget {
	return config.PreviewBudget{CPUs: 1.5, MemoryBytes: 128 << 20, Pids: 32, TTL: time.Minute, Startup: time.Minute}
}
