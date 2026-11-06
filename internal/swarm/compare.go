package swarm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Novirmor/homonto/internal/workspace"
	"github.com/Novirmor/homonto/internal/worktask"
)

// CheckRun is one candidate's result for one configured check.
type CheckRun struct {
	Candidate string          `json:"candidate"`
	Check     string          `json:"check"`
	Result    worktask.Result `json:"result"`
	Error     string          `json:"error,omitempty"`
}

// Check runs the same configured integration checks against every
// candidate, one candidate at a time so they never compete for the same
// ports or caches. The parent must have paused every candidate's writers.
func (s *Store) Check(ctx context.Context, name, only string, timeout time.Duration, output io.Writer) ([]CheckRun, error) {
	sw, tasks, _, err := s.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	if sw.Cleaned {
		return nil, fmt.Errorf("swarm %s was cleaned up", name)
	}
	checks := s.Tasks.Config.IntegrationChecksFor(sw.Changed)
	if only != "" {
		if !slices.Contains(checks, only) {
			return nil, fmt.Errorf("integration check %q does not match this swarm", only)
		}
		checks = []string{only}
	}
	var runs []CheckRun
	var failures []error
	for _, c := range sw.Candidates {
		for _, check := range checks {
			if ctx.Err() != nil {
				return runs, errors.Join(append(failures, ctx.Err())...)
			}
			if output != nil {
				fmt.Fprintf(output, "== %s: %s\n", c.Name, check)
			}
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			r, err := s.Tasks.RunCheck(checkCtx, tasks[c.Name], check, output)
			cancel()
			run := CheckRun{Candidate: c.Name, Check: check, Result: r}
			if err != nil {
				run.Error = err.Error()
				failures = append(failures, fmt.Errorf("%s %s: %w", c.Name, check, err))
			}
			runs = append(runs, run)
		}
	}
	// A failing candidate is a comparison result, not a failed command: the
	// caller compares candidates next. Only interruption fails the run.
	if ctx.Err() != nil {
		return runs, errors.Join(append(failures, ctx.Err())...)
	}
	return runs, nil
}

// DiffStat summarizes a candidate's committed change to one repository.
type DiffStat struct {
	Commits     int  `json:"commits"`
	Files       int  `json:"files"`
	Insertions  int  `json:"insertions"`
	Deletions   int  `json:"deletions"`
	Uncommitted bool `json:"uncommitted"`
}

// CheckView is one check's evidence for a candidate.
type CheckView struct {
	Status      string        `json:"status"`
	Stale       bool          `json:"stale"`
	StaleReason string        `json:"stale_reason,omitempty"`
	ExitCode    int           `json:"exit_code"`
	Duration    time.Duration `json:"duration"`
	Output      string        `json:"output,omitempty"`
}

// CandidateView is one column of the side-by-side comparison.
type CandidateView struct {
	Name      string               `json:"name"`
	Agent     string               `json:"agent"`
	Models    map[string]string    `json:"models"`
	Revisions map[string]string    `json:"revisions"`
	Branches  map[string]string    `json:"branches"`
	Paths     map[string]string    `json:"paths"`
	Diff      map[string]DiffStat  `json:"diff"`
	Checks    map[string]CheckView `json:"checks"`
	Previews  []PreviewState       `json:"previews"`
	Verified  bool                 `json:"verified"`
	// Unverified says why the candidate cannot be selected yet.
	Unverified []string `json:"unverified,omitempty"`
	Selected   bool     `json:"selected"`
}

// Comparison is the whole side-by-side view.
type Comparison struct {
	Swarm         string            `json:"swarm"`
	Engine        string            `json:"engine"`
	Brief         Brief             `json:"brief"`
	Bases         map[string]string `json:"bases"`
	Changed       []string          `json:"changed"`
	Checks        []string          `json:"checks"`
	NeedsFallback bool              `json:"needs_fallback"`
	Candidates    []CandidateView   `json:"candidates"`
	Selected      string            `json:"selected,omitempty"`
	Cleaned       bool              `json:"cleaned"`
	Warning       string            `json:"warning,omitempty"`
}

// Compare builds the comparison from the recorded evidence. It runs nothing.
func (s *Store) Compare(ctx context.Context, name string) (*Comparison, error) {
	sw, tasks, warn, err := s.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.compare(ctx, sw, tasks, warn)
}

func (s *Store) compare(ctx context.Context, sw *Swarm, tasks map[string]*worktask.Task, warn error) (*Comparison, error) {
	checks := s.Tasks.Config.IntegrationChecksFor(sw.Changed)
	cmp := &Comparison{Swarm: sw.Name, Engine: sw.Engine, Brief: sw.Brief, Bases: sw.Bases, Changed: sw.Changed, Checks: checks, NeedsFallback: len(checks) == 0, Cleaned: sw.Cleaned}
	if warn != nil {
		cmp.Warning = warn.Error()
	}
	if sw.Selection != nil {
		cmp.Selected = sw.Selection.Candidate
	}
	for _, c := range sw.Candidates {
		t := tasks[c.Name]
		v := CandidateView{Name: c.Name, Agent: c.Agent, Models: c.Models, Revisions: map[string]string{}, Branches: map[string]string{}, Paths: map[string]string{}, Diff: map[string]DiffStat{}, Checks: map[string]CheckView{}, Selected: cmp.Selected == c.Name}
		for _, alias := range sw.Changed {
			co := t.Checkouts[alias]
			v.Branches[alias], v.Paths[alias] = co.Branch, co.Path
			if co.Removed {
				v.Unverified = append(v.Unverified, alias+" checkout was removed")
				if sw.Selection != nil {
					v.Revisions[alias] = sw.Selection.Revisions[c.Name][alias]
				}
				continue
			}
			stat, head, err := diffStat(ctx, co.Path, co.Base)
			if err != nil {
				return nil, fmt.Errorf("candidate %s %s: %w", c.Name, alias, err)
			}
			v.Revisions[alias], v.Diff[alias] = head, stat
			if stat.Uncommitted {
				v.Unverified = append(v.Unverified, alias+" has uncommitted changes; commit the candidate first")
			}
			if stat.Commits == 0 {
				v.Unverified = append(v.Unverified, alias+" has no commits beyond the base")
			}
		}
		for _, check := range checks {
			r, ok := t.Results[check]
			if !ok {
				v.Checks[check] = CheckView{Status: "not-run"}
				v.Unverified = append(v.Unverified, check+" has not run")
				continue
			}
			view := CheckView{Status: r.Status, Stale: r.Stale, StaleReason: r.StaleReason, ExitCode: r.ExitCode, Output: r.Output}
			if !r.Finished.IsZero() {
				view.Duration = r.Finished.Sub(r.Started).Round(time.Millisecond)
			}
			v.Checks[check] = view
			switch {
			case r.Status != "passed":
				v.Unverified = append(v.Unverified, check+" "+r.Status)
			case r.Stale:
				v.Unverified = append(v.Unverified, check+" is stale: "+r.StaleReason)
			}
		}
		if len(checks) == 0 {
			v.Unverified = append(v.Unverified, "no configured check matches; verify the candidate yourself and select with --fallback-verified")
		}
		for _, key := range sortedPreviewKeys(sw) {
			if p := sw.Previews[key]; p.Candidate == c.Name {
				v.Previews = append(v.Previews, p)
			}
		}
		v.Verified = len(v.Unverified) == 0
		cmp.Candidates = append(cmp.Candidates, v)
	}
	return cmp, nil
}

// diffStat reports commits and changes from base to HEAD, and whether the
// checkout has uncommitted work that the comparison cannot see.
func diffStat(ctx context.Context, dir, base string) (DiffStat, string, error) {
	var st DiffStat
	head, err := workspace.Git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return st, "", err
	}
	count, err := workspace.Git(ctx, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return st, "", err
	}
	st.Commits, _ = strconv.Atoi(strings.TrimSpace(count))
	numstat, err := workspace.Git(ctx, dir, "diff", "--numstat", base, "HEAD")
	if err != nil {
		return st, "", err
	}
	for _, line := range strings.Split(numstat, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		st.Files++
		add, _ := strconv.Atoi(fields[0]) // "-" for binary files
		del, _ := strconv.Atoi(fields[1])
		st.Insertions += add
		st.Deletions += del
	}
	st.Uncommitted = workspace.CheckoutClean(ctx, dir, false) != nil
	return st, head, nil
}

// Select records the user's choice. A candidate is selectable only when every
// configured check passed on its current commits; with no configured check,
// the caller must attest its own verification.
func (s *Store) Select(ctx context.Context, name, candidate string, fallbackVerified bool) (*Swarm, error) {
	unlock, err := s.lock(name, lockWait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sw, err := s.read(name)
	if err != nil {
		return nil, err
	}
	if sw.Cleaned {
		return nil, fmt.Errorf("swarm %s was cleaned up", name)
	}
	if _, ok := sw.Candidate(candidate); !ok {
		return nil, fmt.Errorf("swarm %s has no candidate %q", name, candidate)
	}
	tasks, err := s.loadTasks(ctx, sw)
	if err != nil {
		return nil, err
	}
	cmp, err := s.compare(ctx, sw, tasks, nil)
	if err != nil {
		return nil, err
	}
	sel := &Selection{Candidate: candidate, At: s.now(), Revisions: map[string]map[string]string{}}
	for _, v := range cmp.Candidates {
		sel.Revisions[v.Name] = v.Revisions
		if v.Name != candidate || v.Verified {
			continue
		}
		reasons := v.Unverified
		if cmp.NeedsFallback && fallbackVerified {
			reasons = slices.DeleteFunc(slices.Clone(reasons), func(r string) bool { return strings.HasPrefix(r, "no configured check matches") })
			sel.Fallback = true
		}
		if len(reasons) > 0 {
			return nil, fmt.Errorf("candidate %s is not verified: %s", candidate, strings.Join(reasons, "; "))
		}
	}
	if fallbackVerified && !cmp.NeedsFallback {
		return nil, fmt.Errorf("--fallback-verified applies only when no configured check matches; configured checks decide here")
	}
	sw.Selection = sel
	return sw, s.save(sw)
}

// CleanupReport says what cleanup removed and what it kept.
type CleanupReport struct {
	Removed  []string `json:"removed"`
	Deleted  []string `json:"deleted_branches"`
	Retained []string `json:"retained_branches"`
	Previews []string `json:"stopped_previews"`
}

// Cleanup tears down every preview, then removes the rejected candidates'
// worktrees and deletes their branches when they still point at the revision
// the user rejected, then releases the winner's task as workspace task
// cleanup does. Abandon releases every candidate without a winner and keeps
// all branches.
func (s *Store) Cleanup(ctx context.Context, name string, yes, abandon, keepRejected bool) (*CleanupReport, error) {
	if !yes {
		return nil, fmt.Errorf("swarm cleanup requires --yes")
	}
	unlock, err := s.lock(name, previewLockWait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sw, err := s.read(name)
	if err != nil {
		return nil, err
	}
	if sw.Selection == nil && !abandon {
		return nil, fmt.Errorf("select a winner first (swarm select), or pass --abandon to discard every candidate")
	}
	report := &CleanupReport{Removed: []string{}, Deleted: []string{}, Retained: []string{}, Previews: []string{}}
	// Previews first: nothing may keep serving a checkout that is going away.
	stopped, err := s.stopPreviewsLocked(ctx, sw, nil)
	report.Previews = append(report.Previews, stopped...)
	if err != nil {
		return report, fmt.Errorf("previews could not be torn down; worktrees are kept until they are: %w", err)
	}
	winner := ""
	if sw.Selection != nil && !abandon {
		winner = sw.Selection.Candidate
	}
	order := make([]Candidate, 0, len(sw.Candidates))
	for _, c := range sw.Candidates {
		if c.Name != winner {
			order = append(order, c)
		}
	}
	if c, ok := sw.Candidate(winner); ok {
		order = append(order, c)
	}
	for _, c := range order {
		if !s.taskExists(c.Task) {
			continue // allocation never reached this candidate
		}
		t, err := s.Tasks.Cleanup(ctx, c.Task, true)
		if err != nil {
			return report, fmt.Errorf("candidate %s: %w", c.Name, err)
		}
		report.Removed = append(report.Removed, c.Name)
		for _, alias := range sw.Changed {
			branch := t.Checkouts[alias].Branch
			if branch == "" {
				continue
			}
			ref := "refs/heads/" + branch
			source := t.Checkouts[alias].Source
			tip, err := workspace.Git(ctx, source, "for-each-ref", "--format=%(objectname)", ref)
			if err != nil {
				return report, err
			}
			if tip == "" {
				continue // released as integrated, or already deleted
			}
			rejected := c.Name != winner && !abandon && !keepRejected
			if rejected && sw.Selection.Revisions[c.Name][alias] == tip {
				// The old-value argument makes the deletion fail if the branch
				// moved since the check above.
				if _, err := workspace.Git(ctx, source, "update-ref", "-d", ref, tip); err != nil {
					return report, err
				}
				report.Deleted = append(report.Deleted, alias+":"+branch)
				continue
			}
			report.Retained = append(report.Retained, alias+":"+branch)
		}
	}
	sw.Cleaned = true
	return report, s.save(sw)
}
