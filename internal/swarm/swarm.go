// Package swarm runs a bounded experiment: two or three candidate
// implementations of one brief, each in its own workspace task, compared
// under the same checks before the user chooses one (ADR 0113, ADR 0114).
// It schedules nothing: the parent session dispatches the candidates' agents.
package swarm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/Novirmor/homonto/internal/applylock"
	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/Novirmor/homonto/internal/worktask"
)

// MinCandidates and MaxCandidates bound the experiment (roadmap M6).
const (
	MinCandidates = 2
	MaxCandidates = 3
	maxBriefBytes = 256 << 10
)

// CandidateSpec is one requested candidate: a name and the agent that
// implements it. The agent's routed model is the candidate's fixed model.
type CandidateSpec struct {
	Name  string
	Agent string
	// Models is the agent's model per harness, as routed when the swarm was
	// created. The caller resolves it; this package does not route.
	Models map[string]string
}

// Candidate is one recorded candidate.
type Candidate struct {
	Name   string            `json:"name"`
	Agent  string            `json:"agent"`
	Models map[string]string `json:"models"`
	// Task is the candidate's workspace task, named <swarm>--<candidate>.
	Task string `json:"task"`
}

// Brief is the shared instruction every candidate receives, copied into the
// swarm record so no candidate can be handed a different version.
type Brief struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Selection records the user's choice and the revisions every candidate had
// when it was made, so cleanup deletes only the rejected work that was shown.
type Selection struct {
	Candidate string                       `json:"candidate"`
	At        time.Time                    `json:"at"`
	Fallback  bool                         `json:"fallback_verified,omitempty"`
	Revisions map[string]map[string]string `json:"revisions"`
}

// Swarm is the record under .homonto/swarms/<name>/swarm.json.
type Swarm struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Config  string `json:"config"`
	// Changed is the sorted set of repositories every candidate may write.
	Changed    []string                `json:"changed"`
	Bases      map[string]string       `json:"bases"`
	Brief      Brief                   `json:"brief"`
	Candidates []Candidate             `json:"candidates"`
	Selection  *Selection              `json:"selection,omitempty"`
	Previews   map[string]PreviewState `json:"previews,omitempty"`
	Cleaned    bool                    `json:"cleaned,omitempty"`
	Created    time.Time               `json:"created"`
}

// Candidate returns the named candidate.
func (sw *Swarm) Candidate(name string) (Candidate, bool) {
	for _, c := range sw.Candidates {
		if c.Name == name {
			return c, true
		}
	}
	return Candidate{}, false
}

// Store manages swarms over workspace tasks.
type Store struct {
	Tasks *worktask.Store
	// Docker runs previews; nil uses the docker CLI.
	Docker Docker
	// Probe checks a preview URL is serving; nil uses HTTP GET.
	Probe func(ctx context.Context, url string) error
	// Spawn starts the detached expiry watchdog; nil uses this executable.
	Spawn func(args []string) error
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

var (
	safeName      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	candidateName = regexp.MustCompile(`^[a-z0-9]{1,12}$`)
)

// TaskName is a candidate's workspace task. Candidate names contain no '-',
// so distinct (swarm, candidate) pairs never share a task.
func TaskName(swarm, candidate string) string { return swarm + "--" + candidate }

func (s *Store) dir(name string) (string, error) {
	if !safeName.MatchString(name) {
		return "", fmt.Errorf("invalid swarm name %q: use lowercase letters, digits and '-' (at most 40)", name)
	}
	root := s.Tasks.Layout.ConfigRoot
	dir := filepath.Join(root, ".homonto", "swarms", name)
	if err := fsutil.RequireRealParents(root, dir); err != nil {
		return "", err
	}
	return dir, nil
}

var (
	lockWait        = time.Minute
	previewLockWait = 15 * time.Minute
)

func (s *Store) lock(name string, wait time.Duration) (func(), error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		lock, err := applylock.AcquirePath(filepath.Join(dir, "swarm.lock"), filepath.Join(s.Tasks.Layout.ConfigRoot, ".homonto"))
		if err == nil {
			return func() { _ = lock.Release() }, nil
		}
		if !errors.Is(err, applylock.ErrHeld) || time.Now().After(deadline) {
			return nil, fmt.Errorf("swarm %s is busy: %w", name, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Store) save(sw *Swarm) error {
	dir, err := s.dir(sw.Name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(sw, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlane(filepath.Join(dir, "swarm.json"), append(data, '\n'), 0o600)
}

// read loads the record without validating the candidates' tasks.
func (s *Store) read(name string) (*Swarm, error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "swarm.json")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("swarm %s: %w", name, ErrNotFound)
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected regular metadata file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sw Swarm
	if err := json.Unmarshal(data, &sw); err != nil {
		return nil, err
	}
	if sw.Version != 1 || sw.Name != name || sw.Config != s.Tasks.Layout.ConfigPath || sw.ID != swarmID(sw.Config, name) {
		return nil, fmt.Errorf("swarm %s identity or config no longer matches", name)
	}
	if sw.Previews == nil {
		sw.Previews = map[string]PreviewState{}
	}
	return &sw, nil
}

// swarmID labels this swarm's containers; the config path keeps two projects'
// swarms of the same name apart on one Docker daemon.
func swarmID(config, name string) string {
	sum := sha256.Sum256([]byte(config + "\x00" + name))
	return hex.EncodeToString(sum[:8])
}

// requireEngine accepts only a stateless engine that supports every task
// operation a swarm uses. A native lifecycle binds one checkout per change
// and repository, which cannot hold parallel candidates (ADR 0113).
func (s *Store) requireEngine(name string) error {
	e, err := s.Tasks.Engine(name)
	if err != nil {
		return err
	}
	if e.OwnsLifecycle() {
		return fmt.Errorf("engine %q owns a native lifecycle, which binds one checkout per change and repository; a swarm needs a stateless engine such as no", name)
	}
	for _, op := range []string{catalog.OpStart, catalog.OpResume, catalog.OpIntegrationCheck, catalog.OpRelease} {
		if !e.Supports(op) {
			return fmt.Errorf("engine %q does not support %s, which a swarm needs", name, op)
		}
	}
	return nil
}

// Create records a swarm and allocates one worktree-isolated workspace task
// per candidate from the same base. Repeating it resumes the identical swarm,
// including an interrupted allocation.
func (s *Store) Create(ctx context.Context, name, engine string, changed []string, specs []CandidateSpec, briefFile string) (*Swarm, error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	if err := s.requireEngine(engine); err != nil {
		return nil, err
	}
	if len(specs) < MinCandidates || len(specs) > MaxCandidates {
		return nil, fmt.Errorf("a swarm compares %d or %d candidates, not %d", MinCandidates, MaxCandidates, len(specs))
	}
	if err := validateCandidates(specs); err != nil {
		return nil, err
	}
	changed = slices.Clone(changed)
	slices.Sort(changed)
	changed = slices.Compact(changed)
	if len(changed) == 0 {
		return nil, fmt.Errorf("select at least one changed repository")
	}
	if s.Tasks.Layout.WorktreesDir == "" {
		return nil, fmt.Errorf("configure worktrees.dir: every candidate needs its own worktree")
	}
	brief, err := os.ReadFile(briefFile)
	if err != nil {
		return nil, fmt.Errorf("read brief: %w", err)
	}
	if len(brief) == 0 || len(brief) > maxBriefBytes {
		return nil, fmt.Errorf("brief must be non-empty and at most %d bytes", maxBriefBytes)
	}
	sum := sha256.Sum256(brief)
	digest := hex.EncodeToString(sum[:])

	unlock, err := s.lock(name, lockWait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sw, err := s.read(name)
	switch {
	case err == nil:
		if sw.Engine != engine || !slices.Equal(sw.Changed, changed) || sw.Brief.SHA256 != digest || !sameCandidates(sw.Candidates, specs) {
			return nil, fmt.Errorf("swarm %s already exists with another engine, repositories, brief, candidates or routed models; resume its recorded choice", name)
		}
		if sw.Selection != nil || sw.Cleaned {
			return nil, fmt.Errorf("swarm %s has already selected a candidate; it cannot allocate again", name)
		}
	case errors.Is(err, ErrNotFound):
		sw = &Swarm{Version: 1, ID: swarmID(s.Tasks.Layout.ConfigPath, name), Name: name, Engine: engine, Config: s.Tasks.Layout.ConfigPath, Changed: changed, Created: s.now(), Previews: map[string]PreviewState{}}
		for _, c := range specs {
			if s.taskExists(TaskName(name, c.Name)) {
				return nil, fmt.Errorf("workspace task %s already exists outside this swarm; choose another swarm name", TaskName(name, c.Name))
			}
			sw.Candidates = append(sw.Candidates, Candidate{Name: c.Name, Agent: c.Agent, Models: c.Models, Task: TaskName(name, c.Name)})
		}
		sw.Brief = Brief{Path: filepath.Join(dir, "brief.md"), SHA256: digest}
		if err := fsutil.WriteControlPlane(sw.Brief.Path, brief, 0o600); err != nil {
			return nil, err
		}
		if err := s.save(sw); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return sw, s.allocateCandidates(ctx, sw, engine, changed)
}

// validateCandidates requires uniquely named candidates, each with an agent
// that has a routed model.
func validateCandidates(specs []CandidateSpec) error {
	seen := map[string]bool{}
	for _, c := range specs {
		if !candidateName.MatchString(c.Name) || seen[c.Name] {
			return fmt.Errorf("candidate name %q must be unique lowercase letters and digits (at most 12)", c.Name)
		}
		seen[c.Name] = true
		if c.Agent == "" {
			return fmt.Errorf("candidate %s needs an agent", c.Name)
		}
		if len(c.Models) == 0 {
			return fmt.Errorf("candidate %s: agent %s has no routed model on any harness", c.Name, c.Agent)
		}
	}
	return nil
}

// allocateCandidates creates (or resumes) each candidate's workspace task and
// requires every one to start from the base the first recorded.
func (s *Store) allocateCandidates(ctx context.Context, sw *Swarm, engine string, changed []string) error {
	for _, c := range sw.Candidates {
		t, err := s.Tasks.Create(ctx, c.Task, engine, "", "worktree", changed, nil)
		if err != nil {
			return fmt.Errorf("candidate %s: %w", c.Name, err)
		}
		bases := map[string]string{}
		for alias, checkout := range t.Checkouts {
			bases[alias] = checkout.Base
		}
		if sw.Bases == nil {
			sw.Bases = bases
			if err := s.save(sw); err != nil {
				return err
			}
		} else if !mapsEqual(sw.Bases, bases) {
			return fmt.Errorf("candidate %s started from another base (a source branch moved during creation); run swarm cleanup --abandon and create the swarm again", c.Name)
		}
	}
	return nil
}

// ErrNotFound reports a swarm with no record.
var ErrNotFound = errors.New("no such swarm")

// taskExists reports whether a candidate's workspace task was ever recorded;
// an interrupted create can stop before a later candidate's.
func (s *Store) taskExists(task string) bool {
	_, err := os.Lstat(filepath.Join(s.Tasks.Layout.ConfigRoot, ".homonto", "workspace-tasks", task, "task.json"))
	return err == nil
}

func sameCandidates(recorded []Candidate, specs []CandidateSpec) bool {
	if len(recorded) != len(specs) {
		return false
	}
	for i, c := range recorded {
		if c.Name != specs[i].Name || c.Agent != specs[i].Agent || !mapsEqual(c.Models, specs[i].Models) {
			return false
		}
	}
	return true
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Load returns the swarm and every candidate's task with fresh evidence, after
// reconciling previews with their budgets. A preview reconciliation problem is
// returned as warn so read-only commands still report.
func (s *Store) Load(ctx context.Context, name string) (sw *Swarm, tasks map[string]*worktask.Task, warn error, err error) {
	unlock, err := s.lock(name, lockWait)
	if err != nil {
		return nil, nil, nil, err
	}
	sw, err = s.read(name)
	if err == nil {
		warn = s.reconcileLocked(ctx, sw)
	}
	unlock()
	if err != nil {
		return nil, nil, nil, err
	}
	tasks, err = s.loadTasks(ctx, sw)
	return sw, tasks, warn, err
}

func (s *Store) loadTasks(ctx context.Context, sw *Swarm) (map[string]*worktask.Task, error) {
	tasks := map[string]*worktask.Task{}
	for _, c := range sw.Candidates {
		t, err := s.Tasks.Load(ctx, c.Task)
		if err != nil {
			return nil, fmt.Errorf("candidate %s: %w", c.Name, err)
		}
		if t.Engine != sw.Engine || !slices.Equal(t.Changed, sw.Changed) {
			return nil, fmt.Errorf("candidate %s task no longer matches the swarm", c.Name)
		}
		bases := make(map[string]string, len(t.Checkouts))
		for alias, checkout := range t.Checkouts {
			bases[alias] = checkout.Base
		}
		if sw.Bases == nil || !mapsEqual(sw.Bases, bases) {
			return nil, fmt.Errorf("candidate %s task does not match the pinned swarm bases; abandon the swarm and create it again", c.Name)
		}
		s.Tasks.RefreshResults(ctx, t)
		tasks[c.Name] = t
	}
	return tasks, nil
}
