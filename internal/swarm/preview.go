package swarm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/workspace"
)

// Preview states. Starting is the only state with a live owner: start holds
// the swarm lock throughout, so a reader that holds the lock and still sees
// starting knows the starter was interrupted.
const (
	PreviewStarting    = "starting"
	PreviewRunning     = "running"
	PreviewStopped     = "stopped"
	PreviewExpired     = "expired"
	PreviewExited      = "exited"
	PreviewFailed      = "failed"
	PreviewInterrupted = "interrupted"
)

// PreviewState is one candidate's running (or past) preview.
type PreviewState struct {
	Candidate  string               `json:"candidate"`
	Preview    string               `json:"preview"`
	Repo       string               `json:"repo"`
	State      string               `json:"state"`
	Generation int                  `json:"generation"`
	Container  string               `json:"container"`
	Image      string               `json:"image,omitempty"`
	Built      bool                 `json:"built,omitempty"`
	URL        string               `json:"url,omitempty"`
	Revision   string               `json:"revision,omitempty"`
	Budget     config.PreviewBudget `json:"budget"`
	Started    time.Time            `json:"started"`
	Deadline   time.Time            `json:"deadline"`
	Error      string               `json:"error,omitempty"`
}

func (p PreviewState) live() bool { return p.State == PreviewStarting || p.State == PreviewRunning }

func previewKey(candidate, preview string) string { return candidate + "/" + preview }

func sortedPreviewKeys(sw *Swarm) []string {
	keys := make([]string, 0, len(sw.Previews))
	for k := range sw.Previews {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Container is one container Docker reports for a swarm label.
type Container struct {
	Name       string
	Candidate  string
	Preview    string
	Generation string
	Deadline   string
	Running    bool
}

// RunSpec is everything a preview container is started with. The budgets
// are part of the spec, so no caller can start a preview without them.
type RunSpec struct {
	Name    string
	Image   string
	Command []string
	Labels  map[string]string
	Port    int
	Mount   string // host checkout mounted read-only at /workspace, image mode only
	Budget  config.PreviewBudget
}

// Docker is the preview runtime. The CLI implementation shells out to docker;
// tests use a fake.
type Docker interface {
	Available(ctx context.Context) error
	Build(ctx context.Context, contextDir, dockerfile, tag string, labels map[string]string, out io.Writer) error
	Run(ctx context.Context, spec RunSpec) error
	// HostPort is the loopback port Docker published for the container port.
	HostPort(ctx context.Context, name string, port int) (int, error)
	// List returns every container carrying label=value, running or not.
	List(ctx context.Context, label, value string) ([]Container, error)
	Logs(ctx context.Context, name string, tail int) string
	// Remove force-removes containers; a missing one is not an error.
	Remove(ctx context.Context, names ...string) error
	// RemoveImage removes a built image; a missing one is not an error.
	RemoveImage(ctx context.Context, tag string) error
}

// Labels every preview container carries, so teardown can find containers
// the record never learned about (an interrupted start).
const (
	LabelSwarm      = "io.homonto.swarm"
	LabelCandidate  = "io.homonto.candidate"
	LabelPreview    = "io.homonto.preview"
	LabelGeneration = "io.homonto.generation"
	LabelDeadline   = "io.homonto.deadline"
)

func (s *Store) docker() Docker {
	if s.Docker != nil {
		return s.Docker
	}
	return DockerCLI{}
}

func (s *Store) probe(ctx context.Context, url string) error {
	if s.Probe != nil {
		return s.Probe(ctx, url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}

func containerName(sw *Swarm, candidate, preview string) string {
	return fmt.Sprintf("homonto-%s-%s-%s-%s", sw.ID, sw.Name, candidate, preview)
}

// StartPreviews starts every configured preview for the selected candidates
// (all when none are named), replacing any earlier one. A preview that fails
// to build, start or answer within its startup budget is torn down at once,
// including when the command is interrupted.
func (s *Store) StartPreviews(ctx context.Context, name string, only []string, output io.Writer) ([]PreviewState, error) {
	unlock, err := s.lock(name, previewLockWait)
	if err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	sw, err := s.read(name)
	if err != nil {
		return nil, err
	}
	if sw.Cleaned {
		return nil, fmt.Errorf("swarm %s was cleaned up", name)
	}
	d := s.docker()
	if err := d.Available(ctx); err != nil {
		return nil, fmt.Errorf("previews need a reachable Docker daemon: %w", err)
	}
	if err := s.reconcileLocked(ctx, sw); err != nil {
		return nil, err
	}
	previews := s.Tasks.Config.PreviewsFor(sw.Changed)
	if len(previews) == 0 {
		return nil, fmt.Errorf("no [preview.<name>] declares a repository this swarm changes")
	}
	candidates, err := pick(sw, only)
	if err != nil {
		return nil, err
	}
	var started []PreviewState
	var failures []error
	for _, c := range candidates {
		for _, pname := range previews {
			// Let watchdogs and other commands reconcile between starts; a slow
			// later build must not hold an earlier preview past its deadline.
			unlock()
			locked = false
			unlock, err = s.lock(name, previewLockWait)
			if err != nil {
				return started, errors.Join(append(failures, err)...)
			}
			locked = true
			sw, err = s.read(name)
			if err != nil {
				return started, errors.Join(append(failures, err)...)
			}
			if sw.Cleaned {
				return started, errors.Join(append(failures, fmt.Errorf("swarm %s was cleaned up", name))...)
			}
			if err = s.reconcileLocked(ctx, sw); err != nil {
				return started, errors.Join(append(failures, err)...)
			}
			tasks, err := s.loadTasks(ctx, sw)
			if err != nil {
				return started, errors.Join(append(failures, err)...)
			}
			p, err := s.startOne(ctx, sw, c, pname, s.Tasks.Config.Preview[pname], tasks[c.Name].Checkouts[s.Tasks.Config.Preview[pname].Repo].Path, output)
			started = append(started, p)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s %s: %w", c.Name, pname, err))
			}
			if ctx.Err() != nil {
				return started, errors.Join(append(failures, ctx.Err())...)
			}
		}
	}
	return started, errors.Join(failures...)
}

func pick(sw *Swarm, only []string) ([]Candidate, error) {
	if len(only) == 0 {
		return sw.Candidates, nil
	}
	var out []Candidate
	for _, name := range only {
		c, ok := sw.Candidate(name)
		if !ok {
			return nil, fmt.Errorf("swarm %s has no candidate %q", sw.Name, name)
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) startOne(ctx context.Context, sw *Swarm, c Candidate, pname string, cfg config.Preview, checkout string, output io.Writer) (PreviewState, error) {
	d := s.docker()
	key := previewKey(c.Name, pname)
	prev := sw.Previews[key]
	// Replace an earlier preview; its generation is superseded below.
	if err := s.teardown(context.WithoutCancel(ctx), prev); err != nil {
		return prev, err
	}
	budget := cfg.Budget()
	now := s.now()
	p := PreviewState{Candidate: c.Name, Preview: pname, Repo: cfg.Repo, State: PreviewStarting, Generation: prev.Generation + 1, Container: containerName(sw, c.Name, pname), Budget: budget, Started: now, Deadline: now.Add(budget.TTL)}
	if rev, err := gitHead(ctx, checkout); err == nil {
		p.Revision = rev
	}
	if cfg.Dockerfile != "" {
		p.Image, p.Built = fmt.Sprintf("homonto-preview-%s-%s-%s:%d", sw.ID, c.Name, pname, p.Generation), true
	} else {
		p.Image = cfg.Image
	}
	// Persist intent before Docker runs, so an interrupted start leaves a
	// record naming exactly what to remove.
	sw.Previews[key] = p
	if err := s.save(sw); err != nil {
		return p, err
	}
	fail := func(state string, cause error) (PreviewState, error) {
		cleanup := context.WithoutCancel(ctx)
		if logs := d.Logs(cleanup, p.Container, 20); logs != "" {
			cause = fmt.Errorf("%w\n%s", cause, logs)
		}
		p.Error = cause.Error()
		if err := s.teardown(cleanup, p); err != nil {
			cause = errors.Join(cause, err)
			p.Error = cause.Error()
		} else {
			p.State = state
		}
		p.URL = ""
		sw.Previews[key] = p
		return p, errors.Join(cause, s.save(sw))
	}
	// Arm each deadline before Docker can create anything. If the watchdog
	// cannot start, do not create an unbounded container.
	if err := s.spawn(sw.Name, []string{"workspace", "swarm", "preview", "reap", sw.Name, "--config", s.Tasks.Layout.ConfigPath, "--until", p.Deadline.Format(time.RFC3339Nano)}); err != nil {
		return fail(PreviewFailed, fmt.Errorf("start preview expiry watchdog: %w", err))
	}
	startCtx, cancel := context.WithTimeout(ctx, min(budget.Startup, budget.TTL))
	defer cancel()
	// The lock prevents a watchdog from reaping during this start. Stop a
	// slow startup in time for the earliest already-running preview instead.
	for otherKey, other := range sw.Previews {
		if otherKey != key && other.live() {
			var stop context.CancelFunc
			startCtx, stop = context.WithDeadline(startCtx, other.Deadline)
			defer stop()
		}
	}
	interrupted := func(err error) (PreviewState, error) {
		if ctx.Err() != nil {
			return fail(PreviewInterrupted, err)
		}
		return fail(PreviewFailed, err)
	}
	labels := map[string]string{LabelSwarm: sw.ID, LabelCandidate: c.Name, LabelPreview: pname, LabelGeneration: fmt.Sprint(p.Generation), LabelDeadline: fmt.Sprint(p.Deadline.Unix())}
	spec := RunSpec{Name: p.Container, Image: p.Image, Command: cfg.Command, Labels: labels, Port: cfg.Port, Budget: budget}
	if cfg.Dockerfile != "" {
		dockerfile, contextDir, err := inside(checkout, cfg.Dockerfile, cfg.ContextOrDefault())
		if err != nil {
			return fail(PreviewFailed, err)
		}
		if err := d.Build(startCtx, contextDir, dockerfile, p.Image, labels, output); err != nil {
			return interrupted(fmt.Errorf("build: %w", err))
		}
	} else {
		spec.Mount = checkout
	}
	if err := d.Run(startCtx, spec); err != nil {
		return interrupted(fmt.Errorf("run: %w", err))
	}
	port, err := d.HostPort(startCtx, p.Container, cfg.Port)
	if err != nil {
		return interrupted(err)
	}
	p.URL = fmt.Sprintf("http://127.0.0.1:%d%s", port, cfg.PathOrDefault())
	for {
		err := s.probe(startCtx, p.URL)
		if err == nil {
			break
		}
		if startCtx.Err() != nil {
			return interrupted(fmt.Errorf("not serving %s within the %s startup budget: %w", p.URL, budget.Startup, err))
		}
		select {
		case <-startCtx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
	p.State = PreviewRunning
	sw.Previews[key] = p
	return p, s.save(sw)
}

func gitHead(ctx context.Context, dir string) (string, error) {
	return workspace.Git(ctx, dir, "rev-parse", "HEAD")
}

// inside resolves the Dockerfile and build context within the checkout.
func inside(checkout, dockerfile, contextDir string) (string, string, error) {
	root, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return "", "", err
	}
	var out []string
	for _, rel := range []string{dockerfile, contextDir} {
		p, err := filepath.EvalSymlinks(filepath.Join(root, rel))
		if err != nil {
			return "", "", err
		}
		r, err := filepath.Rel(root, p)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("%s escapes the candidate checkout", rel)
		}
		out = append(out, p)
	}
	return out[0], out[1], nil
}

// teardown removes a preview's container and built image.
func (s *Store) teardown(ctx context.Context, p PreviewState) error {
	if p.Container == "" {
		return nil
	}
	d := s.docker()
	if err := d.Remove(ctx, p.Container); err != nil {
		return err
	}
	if p.Built && p.Image != "" {
		return d.RemoveImage(ctx, p.Image)
	}
	return nil
}

// StopPreviews tears down the selected candidates' previews (all when none
// are named).
func (s *Store) StopPreviews(ctx context.Context, name string, only []string) ([]string, error) {
	unlock, err := s.lock(name, previewLockWait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sw, err := s.read(name)
	if err != nil {
		return nil, err
	}
	if _, err := pick(sw, only); err != nil {
		return nil, err
	}
	return s.stopPreviewsLocked(ctx, sw, only)
}

// stopPreviewsLocked removes recorded previews and every container that
// carries this swarm's label, recorded or not.
func (s *Store) stopPreviewsLocked(ctx context.Context, sw *Swarm, only []string) ([]string, error) {
	if len(sw.Previews) == 0 {
		return nil, nil
	}
	ctx = context.WithoutCancel(ctx)
	want := func(candidate string) bool { return len(only) == 0 || slices.Contains(only, candidate) }
	anyRecorded := false
	for _, p := range sw.Previews {
		if want(p.Candidate) && (p.live() || p.Built) {
			anyRecorded = true
		}
	}
	d := s.docker()
	if err := d.Available(ctx); err != nil {
		if anyRecorded {
			return nil, fmt.Errorf("docker is unreachable: %w", err)
		}
		return nil, nil // nothing was ever started, so nothing can be running
	}
	var stopped []string
	for _, key := range sortedPreviewKeys(sw) {
		p := sw.Previews[key]
		if !want(p.Candidate) {
			continue
		}
		if err := s.teardown(ctx, p); err != nil {
			return stopped, err
		}
		if p.live() {
			stopped = append(stopped, key)
			p.State, p.URL = PreviewStopped, ""
		}
		p.Built = false // its image is gone
		sw.Previews[key] = p
	}
	orphans, err := d.List(ctx, LabelSwarm, sw.ID)
	if err != nil {
		return stopped, err
	}
	for _, c := range orphans {
		if !want(c.Candidate) {
			continue
		}
		if err := d.Remove(ctx, c.Name); err != nil {
			return stopped, err
		}
		stopped = append(stopped, c.Name)
	}
	return stopped, s.save(sw)
}

// reconcileLocked enforces the time budget and repairs the record after an
// interruption: it removes expired previews, previews whose start was
// interrupted, and containers the record does not own, and marks previews
// whose container is gone. The caller holds the swarm lock.
func (s *Store) reconcileLocked(ctx context.Context, sw *Swarm) error {
	// Start records intent before Docker runs, so a swarm with no recorded
	// preview has no container to find; it never needs Docker.
	if len(sw.Previews) == 0 {
		return nil
	}
	live := false
	for _, p := range sw.Previews {
		live = live || p.live()
	}
	d := s.docker()
	if err := d.Available(ctx); err != nil {
		if live {
			return fmt.Errorf("previews are recorded as running but docker is unreachable: %w", err)
		}
		return nil
	}
	now := s.now()
	containers, err := d.List(ctx, LabelSwarm, sw.ID)
	if err != nil {
		return err
	}
	present := map[string]Container{}
	for _, c := range containers {
		present[c.Name] = c
	}
	changed := false
	for _, key := range sortedPreviewKeys(sw) {
		p := sw.Previews[key]
		if !p.live() {
			continue
		}
		c, ok := present[p.Container]
		next := p.State
		switch {
		case p.State == PreviewStarting:
			next = PreviewInterrupted
		case !now.Before(p.Deadline):
			next = PreviewExpired
		case !ok || c.Generation != fmt.Sprint(p.Generation) || !c.Running:
			next = PreviewExited
		}
		if next == p.State {
			delete(present, p.Container)
			continue
		}
		if err := s.teardown(ctx, p); err != nil {
			return err
		}
		p.State, p.URL = next, ""
		sw.Previews[key] = p
		changed = true
	}
	// Whatever remains carries this swarm's label but no live record: an
	// interrupted start, or a superseded generation.
	for name := range present {
		if err := d.Remove(ctx, name); err != nil {
			return err
		}
	}
	if changed {
		return s.save(sw)
	}
	return nil
}

// Reap waits until the given time (if any) and then reconciles previews; the
// detached watchdog runs it to enforce the time budget.
func (s *Store) Reap(ctx context.Context, name string, until time.Time) error {
	if wait := time.Until(until); wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	unlock, err := s.lock(name, previewLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	sw, err := s.read(name)
	if err != nil {
		return err
	}
	return s.reconcileLocked(ctx, sw)
}

func (s *Store) spawn(name string, args []string) error {
	if s.Spawn != nil {
		return s.Spawn(args)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := s.dir(name)
	if err != nil {
		return err
	}
	// The watchdog outlives this command, so its errors go to a log beside
	// the record rather than to a terminal that may be gone.
	log, err := os.OpenFile(filepath.Join(dir, "watchdog.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = log, log
	// A new session: the watchdog survives the terminal and this command.
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
