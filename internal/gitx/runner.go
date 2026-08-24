// Package gitx runs git as a plain subprocess, never through a shell, with
// a bounded timeout and a pinned environment (GIT_TERMINAL_PROMPT=0 so git
// can never block on credentials, LC_ALL=C so diagnostics are stable,
// GIT_EDITOR=true so no invocation can block on an editor). It provides the
// workflow's git plumbing: repository inspection, isolated worktrees for
// implementer assignments, integration worktrees that cherry-pick commit
// materials, clean-baseline fingerprints, and journaled recovery for every
// git side effect.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds one git invocation when a Runner carries no
// explicit timeout.
const DefaultTimeout = 30 * time.Second

// Runner runs one git invocation in dir with args (no shell). Implementations
// must force GIT_TERMINAL_PROMPT=0 and LC_ALL=C and bound the run time.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// ExecRunner is the Runner that shells out to the git binary. The zero
// value uses "git" with DefaultTimeout.
type ExecRunner struct {
	// Git is the git binary path; empty means "git".
	Git string
	// Timeout bounds each invocation; <= 0 means DefaultTimeout.
	Timeout time.Duration
}

// Run executes git with args in dir and returns stdout. A non-zero exit
// yields a *CommandError carrying the exit code and stderr.
func (r ExecRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	bin := r.Git
	if bin == "" {
		bin = "git"
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = nil // /dev/null: a prompt would block, and prompts are disabled anyway
	// A grandchild that inherits the output pipes (e.g. a shell script's
	// background child) must not be able to stretch the run past its
	// deadline: WaitDelay forces I/O collection closed shortly after the
	// process is killed.
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), &CommandError{
				Dir:      dir,
				Args:     args,
				ExitCode: exitErr.ExitCode(),
				Stderr:   stderr.String(),
			}
		}
		return "", fmt.Errorf("gitx: run git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return stdout.String(), nil
}

// CommandError describes a git invocation that exited non-zero.
type CommandError struct {
	Dir      string
	Args     []string
	ExitCode int
	Stderr   string
}

// Error renders the invocation, exit code, and stderr.
func (e *CommandError) Error() string {
	return fmt.Sprintf("gitx: git %s in %s: exit %d: %s",
		strings.Join(e.Args, " "), e.Dir, e.ExitCode, strings.TrimSpace(e.Stderr))
}

// IsNotRepository reports whether err is git's "not a git repository"
// refusal, so callers can treat it as a negative probe rather than a
// failure.
func IsNotRepository(err error) bool {
	var ce *CommandError
	if !errors.As(err, &ce) {
		return false
	}
	return ce.ExitCode == 128 && strings.Contains(ce.Stderr, "not a git repository")
}

// gitEnv returns the ambient environment with LC_ALL, GIT_TERMINAL_PROMPT,
// and GIT_EDITOR overridden to pinned values. GIT_EDITOR=true makes any git
// invocation that would open an editor (for example cherry-pick --continue
// after a conflict resolution) succeed immediately instead of blocking on
// an interactive prompt; Homonto never wants an editor session.
func gitEnv() []string {
	const (
		lcAll    = "LC_ALL="
		terminal = "GIT_TERMINAL_PROMPT="
		editor   = "GIT_EDITOR="
	)
	ambient := os.Environ()
	env := make([]string, 0, len(ambient)+3)
	for _, e := range ambient {
		if strings.HasPrefix(e, lcAll) || strings.HasPrefix(e, terminal) || strings.HasPrefix(e, editor) {
			continue
		}
		env = append(env, e)
	}
	return append(env, terminal+"0", lcAll+"C", editor+"true")
}
