package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxStreamBytes bounds each captured stream. A check that floods its
// output cannot exhaust memory or the runtime database; the result records
// that it was truncated, so the evidence never pretends to be complete.
const MaxStreamBytes = 1 << 20 // 1 MiB

// ErrOutsideMember reports a working directory that escapes the member.
var ErrOutsideMember = errors.New("verify: working directory escapes the member root")

// ErrCommandNotFound reports an argv[0] that could not be resolved under
// the check's own environment.
var ErrCommandNotFound = errors.New("verify: command not found under the allowlisted environment")

// Clock is the runner's time source; tests inject a fixed one.
type Clock func() time.Time

// Runner executes checks against one member's working tree.
type Runner struct {
	root   string
	lookup func(string) (string, bool)
	now    Clock
}

// Options configure a Runner. Zero values select the process defaults.
type Options struct {
	// Lookup resolves an environment variable name to its ambient value.
	// Defaults to os.LookupEnv; tests inject a fixed environment.
	Lookup func(string) (string, bool)
	// Now is the clock stamped into results.
	Now Clock
}

// NewRunner binds a runner to the absolute root of the member whose checks
// it runs.
func NewRunner(root string, opts Options) (*Runner, error) {
	if root == "" {
		return nil, fmt.Errorf("verify: member root must not be empty")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("verify: member root %q must be an absolute path", root)
	}
	r := &Runner{root: root, lookup: opts.Lookup, now: opts.Now}
	if r.lookup == nil {
		r.lookup = os.LookupEnv
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

// Run executes every spec in order against inputs and returns the whole
// set. Checks run sequentially and in configured order so the evidence is
// reproducible; a failing check does not stop the pass, because knowing
// which checks fail is the point of running them all.
//
// Run itself does not fail on a failing check — it fails only when the
// inputs are unusable, which would make the evidence meaningless.
func (r *Runner) Run(ctx context.Context, inputs Inputs, specs []Spec) (Set, error) {
	if err := inputs.Validate(); err != nil {
		return Set{}, err
	}
	set := Set{Inputs: inputs.canonical(), At: r.now().UTC()}
	for _, spec := range specs {
		result, err := r.runOne(ctx, spec)
		if err != nil {
			return Set{}, err
		}
		set.Results = append(set.Results, result)
	}
	return set, nil
}

// runOne executes one check. Only an un-runnable spec (a bad directory, an
// unresolvable command) returns an error; everything the command itself
// does becomes an Outcome.
func (r *Runner) runOne(ctx context.Context, spec Spec) (Result, error) {
	pin, err := spec.Digest()
	if err != nil {
		return Result{}, err
	}
	result := Result{Spec: spec, SpecPin: pin, StartedAt: r.now().UTC()}

	prep, err := r.prepareCheck(spec)
	if err != nil {
		return Result{}, err
	}
	return r.executeCheck(ctx, result, prep), nil
}

// preparedCheck is a spec resolved into its execution envelope: where the
// command runs, the environment built for it (with the values that must be
// redacted back out of its output), the resolved argv[0], and the timeout
// bounding it.
type preparedCheck struct {
	argv0   string
	dir     string
	env     []string
	secrets []string
	timeout time.Duration
}

// prepareCheck assembles the check's runtime envelope: working directory,
// allowlisted environment, argv[0] resolved under that environment alone,
// and the effective timeout. Failures here are the un-runnable-spec
// errors that abort the whole run; past this point everything is an
// Outcome.
func (r *Runner) prepareCheck(spec Spec) (preparedCheck, error) {
	dir, err := r.workingDir(spec)
	if err != nil {
		return preparedCheck{}, err
	}
	env, secrets := r.environment(spec)
	argv0, err := resolveCommand(spec.Command[0], env)
	if err != nil {
		return preparedCheck{}, fmt.Errorf("verify: check %q: %w", spec.Name, err)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return preparedCheck{argv0: argv0, dir: dir, env: env, secrets: secrets, timeout: timeout}, nil
}

// executeCheck runs the prepared command under its timeout and stamps the
// duration. A command that never starts is an Error outcome — not a
// failed run — because everything past preparation is evidence.
func (r *Runner) executeCheck(ctx context.Context, result Result, prep preparedCheck) Result {
	runCtx, cancel := context.WithTimeout(ctx, prep.timeout)
	defer cancel()

	cmd := exec.Command(prep.argv0, result.Spec.Command[1:]...)
	cmd.Dir = prep.dir
	cmd.Env = prep.env
	cmd.Stdin = nil
	stdout := &boundedBuffer{limit: MaxStreamBytes}
	stderr := &boundedBuffer{limit: MaxStreamBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	isolate(cmd)

	start := r.now()
	if err := cmd.Start(); err != nil {
		result.Outcome = OutcomeError
		result.ExitCode = -1
		result.Error = fmt.Sprintf("start: %v", err)
		result.Duration = r.now().Sub(start)
		result.Summary = summarize(nil, nil, false)
		return result
	}
	fin := awaitCommand(cmd, runCtx, stdout, stderr)
	result.Duration = r.now().Sub(start)
	return interpretCommand(result, fin, prep.secrets, prep.timeout)
}

// finishedCommand is one ended execution: what Wait reported, whether the
// deadline fired (and the context error that tells timeout from
// cancellation), and the captured streams.
type finishedCommand struct {
	err    error
	killed bool
	ctxErr error
	stdout *boundedBuffer
	stderr *boundedBuffer
}

// awaitCommand waits for the command, killing its whole process group the
// moment the bound expires — so a command that ignores its context, or a
// child that outlives it, cannot keep the pass waiting.
func awaitCommand(cmd *exec.Cmd, runCtx context.Context, stdout, stderr *boundedBuffer) finishedCommand {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	fin := finishedCommand{stdout: stdout, stderr: stderr}
	select {
	case fin.err = <-done:
	case <-runCtx.Done():
		fin.killed = true
		_ = killGroup(cmd)
		fin.err = <-done
	}
	fin.ctxErr = runCtx.Err()
	return fin
}

// interpretCommand folds a finished execution into the result: the raw
// streams are redacted and summarized, then the outcome switch turns the
// kill reason and exit status into Passed, Failed, Timeout, or Error.
func interpretCommand(result Result, fin finishedCommand, secrets []string, timeout time.Duration) Result {
	out, oTrunc := fin.stdout.bytes()
	errOut, eTrunc := fin.stderr.bytes()
	result.Stdout = clean(out, secrets)
	result.Stderr = clean(errOut, secrets)
	result.Summary = summarize(result.Stdout, result.Stderr, oTrunc || eTrunc)

	switch {
	case fin.killed && errors.Is(fin.ctxErr, context.DeadlineExceeded):
		result.Outcome = OutcomeTimeout
		result.ExitCode = -1
		result.Error = fmt.Sprintf("timed out after %s", timeout)
		if !processGroupSupported {
			result.Error += "; process groups are unavailable on this platform, so children may survive"
		}
	case fin.killed:
		result.Outcome = OutcomeError
		result.ExitCode = -1
		result.Error = fmt.Sprintf("cancelled: %v", fin.ctxErr)
	case fin.err == nil:
		result.Outcome = OutcomePassed
		result.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(fin.err, &exitErr) {
			result.Outcome = OutcomeFailed
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Outcome = OutcomeError
			result.ExitCode = -1
			result.Error = fin.err.Error()
		}
	}
	return result
}

// workingDir resolves a spec's member-relative directory and refuses one
// that escapes the member root.
func (r *Runner) workingDir(spec Spec) (string, error) {
	if spec.WorkingDir == "" || spec.WorkingDir == "." {
		return r.root, nil
	}
	if filepath.IsAbs(spec.WorkingDir) || strings.Contains(spec.WorkingDir, `\`) {
		return "", fmt.Errorf("verify: check %q: working_dir %q must be a relative slash path: %w",
			spec.Name, spec.WorkingDir, ErrOutsideMember)
	}
	clean := filepath.Clean(filepath.FromSlash(spec.WorkingDir))
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("verify: check %q: working_dir %q: %w", spec.Name, spec.WorkingDir, ErrOutsideMember)
		}
	}
	return filepath.Join(r.root, clean), nil
}

// environment builds the command's environment from the allowlist alone —
// nothing ambient leaks in — and returns the forwarded values so they can
// be redacted back out of the output.
func (r *Runner) environment(spec Spec) (env []string, secrets []string) {
	for _, name := range spec.Environment {
		value, ok := r.lookup(name)
		if !ok {
			continue
		}
		env = append(env, name+"="+value)
		secrets = append(secrets, value)
	}
	return env, secrets
}

// resolveCommand resolves argv[0] against the check's OWN environment. A
// path with a separator is used as given; a bare name is looked up in the
// allowlisted PATH, and refused when the check did not allowlist one —
// silently borrowing the parent's PATH would make the run depend on
// something the evidence does not record.
func resolveCommand(name string, env []string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) || strings.ContainsRune(name, '/') {
		return name, nil
	}
	path, ok := envValue(env, "PATH")
	if !ok {
		return "", fmt.Errorf("%q is a bare command name but PATH is not allowlisted (allowlist PATH or use an absolute path): %w",
			name, ErrCommandNotFound)
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%q was not found in the allowlisted PATH: %w", name, ErrCommandNotFound)
}

// envValue reads one variable out of a built environment.
func envValue(env []string, name string) (string, bool) {
	prefix := name + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix), true
		}
	}
	return "", false
}

// boundedBuffer collects at most limit bytes and reports whether it had to
// drop any. It is written from the command's I/O goroutines, so it locks.
type boundedBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	limit   int
	dropped bool
}

// Write stores what fits and counts the rest as dropped. It always reports
// the full length as written, so the command is never told its output was
// short and never sees a spurious EPIPE.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.dropped = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.dropped = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// bytes returns the captured output and whether anything was dropped.
func (b *boundedBuffer) bytes() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...), b.dropped
}

var _ io.Writer = (*boundedBuffer)(nil)
