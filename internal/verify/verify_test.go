package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// shell is the interpreter the fixture checks are written against. The
// checks themselves are still argv — /bin/sh is argv[0], never a shell
// string handed to the runner.
const shell = "/bin/sh"

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("fixture checks need a POSIX shell")
	}
}

func mustRepoID(t *testing.T) identity.RepositoryID {
	t.Helper()
	id, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	return id
}

// testInputs is a valid, minimal input anchor.
func testInputs(t *testing.T) Inputs {
	t.Helper()
	return Inputs{
		Repository: mustRepoID(t),
		Config:     fingerprint.Bytes("test-config", []byte("v1")),
		Sources:    []fingerprint.Digest{fingerprint.Bytes("test-source", []byte("a"))},
	}
}

// script writes an executable shell script into dir and returns a spec
// that runs it with the given environment allowlist.
func script(t *testing.T, dir, name, body string, env ...string) Spec {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!"+shell+"\n"+body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return Spec{
		Name:        name,
		Command:     []string{path},
		Environment: env,
		Timeout:     10 * time.Second,
	}
}

func newRunner(t *testing.T, root string, env map[string]string) *Runner {
	t.Helper()
	r, err := NewRunner(root, Options{
		Lookup: func(name string) (string, bool) { v, ok := env[name]; return v, ok },
		Now:    time.Now,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

func TestNewRunnerRequiresAnAbsoluteRoot(t *testing.T) {
	if _, err := NewRunner("relative", Options{}); err == nil {
		t.Fatal("NewRunner(relative) = nil error, want rejection")
	}
	if _, err := NewRunner("", Options{}); err == nil {
		t.Fatal("NewRunner(\"\") = nil error, want rejection")
	}
}

func TestRunExecutesArgvNotAShell(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "canary"), []byte("untouched"), 0o600); err != nil {
		t.Fatalf("write canary: %v", err)
	}
	r := newRunner(t, root, nil)
	// If the runner ever passed the command through a shell, the
	// metacharacters below would run as commands. As argv they are
	// literal arguments to echo, and the canary survives.
	spec := Spec{
		Name:    "argv-only",
		Command: []string{"/bin/echo", "; rm canary", "&&", "$(whoami)", "|", ">out"},
		Timeout: 10 * time.Second,
	}
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !set.Passed() {
		t.Fatalf("echo did not pass: %+v", set.Results[0])
	}
	if _, err := os.Stat(filepath.Join(root, "canary")); err != nil {
		t.Fatalf("the canary was destroyed: the command went through a shell: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "out")); err == nil {
		t.Fatal("redirection happened: the command went through a shell")
	}
	if !strings.Contains(string(set.Results[0].Stdout), "; rm canary") {
		t.Fatalf("stdout = %q, want the literal arguments", set.Results[0].Stdout)
	}
}

func TestRunUsesTheConfiguredWorkingDirectory(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub", "deep")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	spec := script(t, root, "pwd.sh", "pwd\n")
	spec.WorkingDir = "sub/deep"
	r := newRunner(t, root, nil)
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := strings.TrimSpace(string(set.Results[0].Stdout))
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotResolved != want {
		t.Fatalf("ran in %q, want %q", gotResolved, want)
	}
}

func TestRunRefusesAWorkingDirectoryOutsideTheMember(t *testing.T) {
	root := t.TempDir()
	r := newRunner(t, root, nil)
	for _, dir := range []string{"../escape", "sub/../../escape", "/etc"} {
		spec := Spec{Name: "escape", Command: []string{"/bin/true"}, WorkingDir: dir, Timeout: time.Second}
		if _, err := r.Run(t.Context(), testInputs(t), []Spec{spec}); !errors.Is(err, ErrOutsideMember) {
			t.Errorf("Run(working_dir=%q) error = %v, want ErrOutsideMember", dir, err)
		}
	}
}

func TestRunForwardsOnlyTheAllowlistedEnvironment(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	spec := script(t, root, "env.sh", "env\n", "ALLOWED")
	r := newRunner(t, root, map[string]string{
		"ALLOWED": "allowed-value-here",
		"SECRET":  "super-secret-value",
	})
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := string(set.Results[0].Stdout)
	if strings.Contains(out, "SECRET") || strings.Contains(out, "super-secret-value") {
		t.Fatalf("a non-allowlisted variable reached the command: %q", out)
	}
	if !strings.Contains(out, "ALLOWED=") {
		t.Fatalf("the allowlisted variable did not reach the command: %q", out)
	}
}

// TestRunRedactsForwardedValues proves an allowlisted value that the
// command echoes back does not survive into the recorded evidence.
func TestRunRedactsForwardedValues(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	spec := script(t, root, "leak.sh", "echo \"token is $TOKEN\" >&2\necho \"token is $TOKEN\"\n", "TOKEN")
	r := newRunner(t, root, map[string]string{"TOKEN": "hunter2-not-in-evidence"})
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := set.Results[0]
	for name, stream := range map[string][]byte{"stdout": res.Stdout, "stderr": res.Stderr} {
		if strings.Contains(string(stream), "hunter2-not-in-evidence") {
			t.Fatalf("%s carries the forwarded secret: %q", name, stream)
		}
		if !strings.Contains(string(stream), RedactedMarker) {
			t.Fatalf("%s was not redacted: %q", name, stream)
		}
	}
}

func TestRunRecordsExitStatus(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	pass := script(t, root, "pass.sh", "exit 0\n")
	fail := script(t, root, "fail.sh", "echo boom >&2\nexit 7\n")
	r := newRunner(t, root, nil)
	set, err := r.Run(t.Context(), testInputs(t), []Spec{pass, fail})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(set.Results) != 2 {
		t.Fatalf("got %d results, want 2 — a failing check must not stop the pass", len(set.Results))
	}
	if set.Results[0].Outcome != OutcomePassed || set.Results[0].ExitCode != 0 {
		t.Fatalf("pass result = %+v", set.Results[0])
	}
	if set.Results[1].Outcome != OutcomeFailed || set.Results[1].ExitCode != 7 {
		t.Fatalf("fail result = %+v", set.Results[1])
	}
	if set.Passed() {
		t.Fatal("a set with a failing check must not report passed")
	}
	if len(set.Failures()) != 1 {
		t.Fatalf("Failures = %+v, want one", set.Failures())
	}
}

func TestRunKillsTheProcessGroupOnTimeout(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	marker := filepath.Join(root, "child-survived")
	// The check backgrounds a child that would outlive it, then sleeps
	// past its own timeout. Killing only the shell would leave the child
	// to write the marker; killing the group takes both.
	body := "(sleep 5; echo alive > " + marker + ") &\nsleep 5\n"
	spec := script(t, root, "slow.sh", body)
	spec.Timeout = 300 * time.Millisecond
	r := newRunner(t, root, nil)

	start := time.Now()
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("the timeout did not bound the run: took %s", elapsed)
	}
	res := set.Results[0]
	if res.Outcome != OutcomeTimeout {
		t.Fatalf("outcome = %q, want timeout (error: %s)", res.Outcome, res.Error)
	}
	if !res.Outcome.Blocking() {
		t.Fatal("a timeout must block")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a backgrounded child survived the timeout: the process group was not killed")
	}
}

func TestRunHandlesInvalidUTF8Output(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	spec := script(t, root, "binary.sh", "printf '\\377\\376ok\\377'\n")
	r := newRunner(t, root, nil)
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := set.Results[0].Stdout
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("the readable part of the output was lost: %q", out)
	}
	for _, b := range out {
		if b == 0xff || b == 0xfe {
			t.Fatalf("invalid bytes survived sanitization: %q", out)
		}
	}
	if !isValidUTF8(out) {
		t.Fatalf("stored output is not valid UTF-8: %q", out)
	}
}

func isValidUTF8(b []byte) bool { return strings.ToValidUTF8(string(b), "") == string(b) }

func TestRunTruncatesFloodingOutput(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	// Emit well over the cap.
	spec := script(t, root, "flood.sh", "i=0\nwhile [ $i -lt 40000 ]; do echo 0123456789012345678901234567890123456789; i=$((i+1)); done\n")
	spec.Timeout = 60 * time.Second
	r := newRunner(t, root, nil)
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := set.Results[0]
	if len(res.Stdout) > MaxStreamBytes {
		t.Fatalf("captured %d bytes, want at most %d", len(res.Stdout), MaxStreamBytes)
	}
	if !res.Summary.Truncated {
		t.Fatal("truncation was not recorded, so the evidence claims to be complete")
	}
	if res.Outcome != OutcomePassed {
		t.Fatalf("outcome = %q; truncation must not change the verdict (%s)", res.Outcome, res.Error)
	}
}

func TestRunRefusesABareCommandWithoutAllowlistedPATH(t *testing.T) {
	root := t.TempDir()
	r := newRunner(t, root, map[string]string{"PATH": os.Getenv("PATH")})
	bare := Spec{Name: "bare", Command: []string{"true"}, Timeout: time.Second}
	if _, err := r.Run(t.Context(), testInputs(t), []Spec{bare}); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("Run(bare, no PATH allowlisted) error = %v, want ErrCommandNotFound", err)
	}
	// Allowlisting PATH resolves it.
	requireUnix(t)
	bare.Environment = []string{"PATH"}
	set, err := r.Run(t.Context(), testInputs(t), []Spec{bare})
	if err != nil {
		t.Fatalf("Run(bare, PATH allowlisted): %v", err)
	}
	if set.Results[0].Outcome != OutcomePassed {
		t.Fatalf("outcome = %q (%s)", set.Results[0].Outcome, set.Results[0].Error)
	}
	// A name that is nowhere on the allowlisted PATH is refused.
	missing := Spec{Name: "missing", Command: []string{"definitely-not-a-real-binary-xyz"},
		Environment: []string{"PATH"}, Timeout: time.Second}
	if _, err := r.Run(t.Context(), testInputs(t), []Spec{missing}); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("Run(missing) error = %v, want ErrCommandNotFound", err)
	}
}

func TestRunReportsAnUnstartableCommand(t *testing.T) {
	root := t.TempDir()
	notExecutable := filepath.Join(root, "data.txt")
	if err := os.WriteFile(notExecutable, []byte("not a program"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := newRunner(t, root, nil)
	spec := Spec{Name: "unstartable", Command: []string{notExecutable}, Timeout: time.Second}
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := set.Results[0]
	if res.Outcome != OutcomeError || res.ExitCode != -1 {
		t.Fatalf("result = %+v, want an error outcome", res)
	}
	if !res.Outcome.Blocking() {
		t.Fatal("a check that never ran must block; it proved nothing")
	}
	if res.Error == "" {
		t.Fatal("the failure was not explained")
	}
}

func TestRunRejectsUnusableInputs(t *testing.T) {
	root := t.TempDir()
	r := newRunner(t, root, nil)
	bad := Inputs{Repository: "not-a-uuid", Config: fingerprint.Bytes("x", nil)}
	if _, err := r.Run(t.Context(), bad, nil); err == nil {
		t.Fatal("Run with a malformed repository id = nil error, want rejection")
	}
	bad = testInputs(t)
	bad.Config = "not-a-digest"
	if _, err := r.Run(t.Context(), bad, nil); err == nil {
		t.Fatal("Run with a malformed config digest = nil error, want rejection")
	}
}

func TestRunHonoursCallerCancellation(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	spec := script(t, root, "sleep.sh", "sleep 5\n")
	spec.Timeout = 30 * time.Second
	r := newRunner(t, root, nil)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	set, err := r.Run(ctx, testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancellation did not stop the check")
	}
	if set.Results[0].Outcome != OutcomeError {
		t.Fatalf("outcome = %q, want error (a cancelled check reached no verdict)", set.Results[0].Outcome)
	}
}

// TestPortableCarriesNoOutput proves the boundary the spec draws: raw
// streams are local, and only counts and a digest travel.
func TestPortableCarriesNoOutput(t *testing.T) {
	requireUnix(t)
	root := t.TempDir()
	spec := script(t, root, "talk.sh", "echo secret-looking-content\necho more >&2\n")
	r := newRunner(t, root, nil)
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	portable := set.Portable()
	for _, res := range portable.Results {
		if res.Stdout != nil || res.Stderr != nil {
			t.Fatal("the portable set carries raw output")
		}
		if res.Summary.StdoutBytes == 0 {
			t.Fatal("the portable summary lost the byte count")
		}
		if err := res.Summary.Output.Validate(); err != nil {
			t.Fatalf("summary digest: %v", err)
		}
	}
	encoded, err := set.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	again, err := portable.Digest()
	if err != nil {
		t.Fatalf("Digest(portable): %v", err)
	}
	if encoded != again {
		t.Fatal("the set digest depends on raw output; it must cover the portable form only")
	}
	// The digest identifies the output without containing it.
	if strings.Contains(string(encoded), "secret-looking-content") {
		t.Fatal("the digest carries content")
	}
}

func TestSummaryCountsLines(t *testing.T) {
	s := summarize([]byte("a\nb\nc"), []byte("x\n"), false)
	if s.StdoutLines != 3 || s.StderrLines != 1 {
		t.Fatalf("summary = %+v, want 3 and 1 lines", s)
	}
	if s.StdoutBytes != 5 || s.StderrBytes != 2 {
		t.Fatalf("summary = %+v, want 5 and 2 bytes", s)
	}
	empty := summarize(nil, nil, false)
	if empty.StdoutLines != 0 || empty.StderrLines != 0 {
		t.Fatalf("empty summary = %+v", empty)
	}
}

func TestRedactPrefersLongerSecrets(t *testing.T) {
	got := string(Redact([]byte("value=abcdef-longer-token"), []string{"abcdef", "abcdef-longer-token"}))
	if got != "value="+RedactedMarker {
		t.Fatalf("Redact = %q, want the whole longer secret replaced", got)
	}
	// Very short values are not redacted: they would match everywhere.
	if string(Redact([]byte("home is /x"), []string{"/x"})) != "home is /x" {
		t.Fatal("a two-character value was redacted; that would destroy the output")
	}
}

func TestFromConfigAppliesTheDefaultTimeout(t *testing.T) {
	spec, err := FromConfig(workspacecfg.Check{Name: "unit", Command: []string{"/bin/true"}})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if spec.Timeout != DefaultTimeout {
		t.Fatalf("timeout = %s, want the manifest default %s", spec.Timeout, DefaultTimeout)
	}
	if _, err := FromConfig(workspacecfg.Check{Name: "", Command: []string{"/bin/true"}}); err == nil {
		t.Error("FromConfig with no name = nil error, want rejection")
	}
	if _, err := FromConfig(workspacecfg.Check{Name: "x"}); err == nil {
		t.Error("FromConfig with no command = nil error, want rejection")
	}
	if _, err := FromConfig(workspacecfg.Check{Name: "x", Command: []string{"/bin/true"}, Timeout: "nonsense"}); err == nil {
		t.Error("FromConfig with a bad timeout = nil error, want rejection")
	}
	if _, err := FromConfig(workspacecfg.Check{Name: "x", Command: []string{"/bin/true"}, Environment: []string{"1BAD"}}); err == nil {
		t.Error("FromConfig with a bad env name = nil error, want rejection")
	}
}
