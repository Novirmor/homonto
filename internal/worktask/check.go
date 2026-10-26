package worktask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/fsutil"
)

type Input struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
	GitDir   string `json:"git_dir"`
}

type CheckContext struct {
	Version int              `json:"version"`
	Task    string           `json:"task"`
	Engine  string           `json:"engine"`
	Check   string           `json:"check"`
	Repos   map[string]Input `json:"repos"`
}

type Result struct {
	Status      string           `json:"status"`
	Definition  string           `json:"definition"`
	Inputs      map[string]Input `json:"inputs"`
	Started     time.Time        `json:"started"`
	Finished    time.Time        `json:"finished,omitempty"`
	ExitCode    int              `json:"exit_code"`
	Context     string           `json:"context"`
	Output      string           `json:"output"`
	Error       string           `json:"error,omitempty"`
	Stale       bool             `json:"stale"`
	StaleReason string           `json:"stale_reason,omitempty"`
}

func definition(check config.IntegrationCheck) string {
	data, _ := json.Marshal(check)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *Store) inputs(ctx context.Context, t *Task, check config.IntegrationCheck) (map[string]Input, error) {
	inputs := make(map[string]Input, len(check.Repos))
	for _, alias := range check.Repos {
		c, ok := t.Checkouts[alias]
		if !ok || c.Removed {
			return nil, fmt.Errorf("%s has no active task checkout; integration inputs changed", alias)
		}
		if c.Removing {
			return nil, fmt.Errorf("%s cleanup is pending; resume workspace task cleanup", alias)
		}
		if !c.Ready {
			return nil, fmt.Errorf("%s allocation is incomplete; rerun workspace task create to resume it or cleanup to abandon it", alias)
		}
		head, err := s.validateCheckout(ctx, t, alias, c)
		if err != nil {
			return nil, err
		}
		if err := clean(ctx, c.Path); err != nil {
			return nil, err
		}
		if !c.Changed && head != c.Base {
			return nil, fmt.Errorf("unchanged input %s moved from pinned revision %s", alias, c.Base)
		}
		inputs[alias] = Input{Path: c.Path, Revision: head, GitDir: c.GitDir}
	}
	return inputs, nil
}

// RefreshResults reports stale evidence without rewriting the original run record.
func (s *Store) RefreshResults(ctx context.Context, t *Task) {
	checks := s.Config.IntegrationChecksFor(t.Changed)
	t.NeedsFallback = len(checks) == 0
	for name, result := range t.Results {
		result.Stale, result.StaleReason = false, ""
		check, ok := s.Config.Integration[name]
		switch {
		case !ok || !slices.Contains(checks, name) || result.Definition != definition(check):
			result.StaleReason = "check definition or selection changed"
		case result.Status == "running":
			result.StaleReason = "run has no completed result"
		default:
			inputs, err := s.inputs(ctx, t, check)
			if err != nil {
				result.StaleReason = err.Error()
			} else if !reflect.DeepEqual(inputs, result.Inputs) {
				result.StaleReason = "candidate revisions or checkout identities changed"
			}
		}
		result.Stale = result.StaleReason != ""
		t.Results[name] = result
	}
}

// snapshotTimeout bounds the post-run input snapshot, which cannot use the
// check's own (possibly expired) deadline.
const snapshotTimeout = time.Minute

// RunCheck requires the parent to pause participating writers. Pre/post snapshots
// detect observed changes; this deliberately does not reserve or sandbox checkouts.
func (s *Store) RunCheck(ctx context.Context, t *Task, name string, output io.Writer) (Result, error) {
	check, ok := s.Config.Integration[name]
	if !ok || !slices.Contains(s.Config.IntegrationChecksFor(t.Changed), name) {
		return Result{}, fmt.Errorf("integration check %q does not match this task", name)
	}
	dir, err := s.dir(t.Name)
	if err != nil {
		return Result{}, err
	}
	// Merge only this check's result, so concurrent checks keep each other's.
	record := func(r Result, wait time.Duration) error {
		t.Results[name] = r
		return s.update(t.Name, wait, func(cur *Task) { cur.Results[name] = r })
	}
	// A failed preflight must supersede an earlier pass, too.
	r := Result{Status: "failed", Definition: definition(check), Started: time.Now().UTC(), ExitCode: -1}
	finish := func(runErr error) (Result, error) {
		r.Finished = time.Now().UTC()
		if runErr != nil {
			r.Status, r.Error = "failed", runErr.Error()
		}
		return r, errors.Join(runErr, record(r, resultLockWait))
	}
	r.Inputs, err = s.inputs(ctx, t, check)
	if err != nil {
		return finish(err)
	}
	root := r.Inputs[check.Repo].Path
	cwd, err := filepath.EvalSymlinks(filepath.Join(root, check.Cwd))
	if err != nil {
		return finish(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return finish(err)
	}
	rel, err := filepath.Rel(realRoot, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return finish(fmt.Errorf("integration cwd escapes execution checkout"))
	}
	runDir, err := os.MkdirTemp(dir, "check-")
	if err != nil {
		return finish(err)
	}
	r.Context, r.Output = filepath.Join(runDir, "context.json"), filepath.Join(runDir, "output.log")
	data, err := json.MarshalIndent(CheckContext{Version: 1, Task: t.Name, Engine: t.Engine, Check: name, Repos: r.Inputs}, "", "  ")
	if err != nil {
		return finish(err)
	}
	if err := fsutil.WriteControlPlane(r.Context, append(data, '\n'), 0o600); err != nil {
		return finish(err)
	}
	log, err := os.OpenFile(r.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return finish(err)
	}
	defer log.Close()
	r.Status = "running"
	if err := record(r, lockWait); err != nil {
		return r, err
	}
	cmd := exec.CommandContext(ctx, check.Command[0], check.Command[1:]...)
	if err := prepareCheckProcess(cmd); err != nil {
		return finish(err)
	}
	cmd.Dir = cwd
	for _, env := range cmd.Environ() {
		if !strings.HasPrefix(env, "GIT_") && !strings.HasPrefix(env, "HOMONTO_INTEGRATION_CONTEXT=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "HOMONTO_INTEGRATION_CONTEXT="+r.Context)
	if output == nil {
		output = io.Discard
	}
	// The check writes to a pipe this process owns, so Wait returns when the
	// check exits, not when every background descendant closes its inherited
	// output. Exec would otherwise copy through its own pipe and report
	// ErrWaitDelay for a check that passed.
	read, write, err := os.Pipe()
	if err != nil {
		return finish(err)
	}
	defer read.Close()
	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.MultiWriter(log, output), read)
		copied <- err
	}()
	cmd.Stdout, cmd.Stderr = write, write
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	write.Close()
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	// Background descendants must not outlive a check that exited by itself.
	stopCheckProcess(cmd)
	// Killed descendants close the pipe. One that left the process group could
	// hold it open, so bound the drain rather than wait for it.
	var copyErr error
	select {
	case copyErr = <-copied:
	case <-time.After(time.Second):
		read.Close()
		<-copied
	}
	// A timeout or interrupt has already ended ctx; the mutation check must
	// still run to report what the check did to its inputs.
	snapshot, cancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotTimeout)
	defer cancel()
	after, inputErr := s.inputs(snapshot, t, check)
	if inputErr == nil && !reflect.DeepEqual(after, r.Inputs) {
		inputErr = fmt.Errorf("participating checkout changed during integration check")
	}
	if inputErr != nil {
		r.Stale, r.StaleReason = true, inputErr.Error()
	}
	r.Status = "passed"
	return finish(errors.Join(runErr, inputErr, copyErr, log.Sync()))
}
