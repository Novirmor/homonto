package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
)

func TestDoctorFlagsMissingSkillContent(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[skills.ghost]\nsource=\"local:ghost\"\nscope=\"user\"\n"), 0o644)

	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(e.Doctor(), "\n")
	if !strings.Contains(lines, "ghost") {
		t.Fatalf("doctor should flag missing skill 'ghost':\n%s", lines)
	}
}

func TestDoctorReportsSkillLinkState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[skills.graphify]\nsource=\"local:graphify\"\nscope=\"user\"\n"), 0o644)
	content := filepath.Join(repo, "content")
	os.MkdirAll(filepath.Join(content, "skills", "graphify"), 0o755)

	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, content)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	// content present but no symlink yet -> not linked
	lines := strings.Join(build().Doctor(), "\n")
	if !strings.Contains(lines, `skill "graphify" content present, not linked`) {
		t.Fatalf("doctor should report unlinked skill:\n%s", lines)
	}

	// correct symlink -> linked
	dst := filepath.Join(home, ".config", "opencode", "skills", "graphify")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Symlink(filepath.Join(content, "skills", "graphify"), dst); err != nil {
		t.Fatal(err)
	}
	lines = strings.Join(build().Doctor(), "\n")
	if !strings.Contains(lines, `ok: skill "graphify" linked`) {
		t.Fatalf("doctor should report linked skill:\n%s", lines)
	}
}

// TestDoctorProjectScopeChecksProjectLocation: with scope=project, doctor must
// verify the tool link at the project root, not the home location.
func TestDoctorProjectScopeChecksProjectLocation(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[skills.graphify]\nsource=\"local:graphify\"\nscope=\"project\"\n"), 0o644)
	content := filepath.Join(repo, "content")
	src := filepath.Join(content, "skills", "graphify")
	os.MkdirAll(src, 0o755)
	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, content)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	// No link yet -> reported unlinked at the project location.
	lines := strings.Join(build().Doctor(), "\n")
	if !strings.Contains(lines, `skill "graphify" content present, not linked for opencode`) {
		t.Fatalf("project-scope doctor should report the project link missing:\n%s", lines)
	}

	// Create the project-location link.
	d := filepath.Join(repo, ".opencode", "skills", "graphify")
	os.MkdirAll(filepath.Dir(d), 0o755)
	if err := os.Symlink(src, d); err != nil {
		t.Fatal(err)
	}
	lines = strings.Join(build().Doctor(), "\n")
	if !strings.Contains(lines, `ok: skill "graphify" linked (opencode)`) {
		t.Fatalf("project-scope doctor should verify the link at the project location:\n%s", lines)
	}
}

// TestDoctorChecksToolConfigLocations: doctor verifies the tool's config
// location exists — absent is a warning naming the path, present is ok.
func TestDoctorChecksToolConfigLocations(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(""), 0o644)
	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	// No config dir yet -> a warning naming the location.
	loc := filepath.Join(home, ".config", "opencode")
	lines := strings.Join(build().Doctor(), "\n")
	if !strings.Contains(lines, loc) || !strings.Contains(lines, "not found") {
		t.Fatalf("doctor should warn about the missing opencode config location:\n%s", lines)
	}

	// Present -> ok.
	if err := os.MkdirAll(loc, 0o755); err != nil {
		t.Fatal(err)
	}
	lines = strings.Join(build().Doctor(), "\n")
	if !strings.Contains(lines, "ok: .config/opencode (OpenCode) config location present") {
		t.Fatalf("doctor should report the opencode config location present:\n%s", lines)
	}
}

// buildStatusEngine wires an engine over the given repo/home with a stubbed
// resolver so secret-free config applies without touching `pass`.
func buildStatusEngine(t *testing.T, repo, home string) *Engine {
	t.Helper()
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	return e
}

func TestStatusDetectsDriftAfterOutOfBandChange(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[settings.opencode]\nmodel=\"opus\"\n"), 0o644)

	e := buildStatusEngine(t, repo, home)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// no drift right after apply
	if d, pending, _ := buildStatusEngine(t, repo, home).Status(); len(d) != 0 || pending != 0 {
		t.Fatalf("unexpected status after clean apply: drift=%v pending=%d", d, pending)
	}

	// change the managed key ON DISK, out of band
	sj := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	os.WriteFile(sj, []byte(`{"model":"sonnet"}`), 0o644)

	drift, pending, err := buildStatusEngine(t, repo, home).Status()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(drift, "\n")
	if len(drift) == 0 || !strings.Contains(joined, "model") || !strings.Contains(joined, "drifted") {
		t.Fatalf("expected drift on model, got %v", drift)
	}
	// The drifted key is reported as drift, not as pending config work.
	if pending != 0 {
		t.Fatalf("a disk-drifted key must not also count as pending, got pending=%d", pending)
	}
}

// TestStatusConfigEditIsPendingNotDrift is the load-bearing negative: a pure
// CONFIG edit (desired changes, disk unchanged) must NOT be reported as disk
// drift — it is a pending config change awaiting apply. The old Plan-based
// Drift mis-reported this as drift; this proves the fix.
func TestStatusConfigEditIsPendingNotDrift(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[settings.opencode]\nmodel=\"opus\"\n"), 0o644)

	e := buildStatusEngine(t, repo, home)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// Edit ONLY the config (desired), leaving the on-disk value untouched.
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[settings.opencode]\nmodel=\"sonnet\"\n"), 0o644)

	drift, pending, err := buildStatusEngine(t, repo, home).Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Fatalf("a pure config edit must not be reported as disk drift, got %v", drift)
	}
	if pending != 1 {
		t.Fatalf("a pure config edit must count as one pending change, got pending=%d", pending)
	}
}

// TestStatusReportsMissingManagedKey: a state-recorded key removed from disk out
// of band is reported as missing (and does not count toward pending).
func TestStatusReportsMissingManagedKey(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[settings.opencode]\nmodel=\"opus\"\n"), 0o644)

	e := buildStatusEngine(t, repo, home)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// remove the managed key from disk out of band
	sj := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	os.WriteFile(sj, []byte(`{}`), 0o644)

	drift, pending, err := buildStatusEngine(t, repo, home).Status()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(drift, "\n")
	if !strings.Contains(joined, "model") || !strings.Contains(joined, "missing") {
		t.Fatalf("deleted managed key must report as missing, got %v", drift)
	}
	if pending != 0 {
		t.Fatalf("a missing (drifted) key must not count as pending, got pending=%d", pending)
	}
}

// TestStatusDriftedKeyExcludedFromPendingWhileOthersCount proves the pending
// exclusion is specific to the drifted key: a key that is BOTH disk-drifted and
// config-edited is reported once as drift and NOT counted in pending, while a
// sibling key that is a pure config edit (disk == Applied) still counts. This
// locks in that pending tallies only OTHER config work, never the drifted key.
func TestStatusDriftedKeyExcludedFromPendingWhileOthersCount(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"),
		[]byte("[settings.opencode]\nmodel=\"opus\"\ntheme=\"dark\"\n"), 0o644)

	e := buildStatusEngine(t, repo, home)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// model: change ON DISK to "sonnet" (disk drift) while editing desired to a
	// DIFFERENT "haiku" — so desired != disk != Applied, both disk-drifted AND a
	// config edit. theme: leave disk at "dark" (== Applied) but edit desired to
	// "light" — a pure config edit that must count as pending.
	sj := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	os.WriteFile(sj, []byte(`{"model":"sonnet","theme":"dark"}`), 0o644)
	os.WriteFile(filepath.Join(repo, "homonto.toml"),
		[]byte("[settings.opencode]\nmodel=\"haiku\"\ntheme=\"light\"\n"), 0o644)

	drift, pending, err := buildStatusEngine(t, repo, home).Status()
	if err != nil {
		t.Fatal(err)
	}
	// model appears exactly once as drift; the pure config edit on theme does not.
	if len(drift) != 1 || !strings.Contains(drift[0], "model") || !strings.Contains(drift[0], "drifted") {
		t.Fatalf("expected exactly one drift entry for model, got %v", drift)
	}
	if strings.Contains(strings.Join(drift, "\n"), "theme") {
		t.Fatalf("a pure config edit on theme must not be reported as drift, got %v", drift)
	}
	// pending counts only the non-drifted config edit (theme); the drifted model
	// key is excluded even though it is also a config change.
	if pending != 1 {
		t.Fatalf("pending must count only the non-drifted config edit, got pending=%d", pending)
	}
}

// TestStatusSkipsErroredAdapterButContinues proves a per-adapter drift-scan
// failure is isolated: a malformed opencode.jsonc makes the adapter's Plan and
// ObserveHashes both fail, so it is skipped with a warning — Status itself
// must not error, and the warning (which the CLI turns into a non-zero exit)
// says the drift scan was skipped rather than silently reporting a clean bill.
func TestStatusSkipsErroredAdapterButContinues(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"),
		[]byte("[settings.opencode]\ntheme=\"dark\"\n"), 0o644)

	e := buildStatusEngine(t, repo, home)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// Corrupt opencode.jsonc so the adapter cannot parse it: both its Plan and
	// ObserveHashes fail, exercising the skip-with-warning path.
	oc := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	os.WriteFile(oc, []byte(`{ not json`), 0o644)

	e2 := buildStatusEngine(t, repo, home)
	_, _, err := e2.Status()
	if err != nil {
		t.Fatal(err)
	}
	// The broken adapter is reported as a warning, not a hard failure.
	if len(e2.Warnings) == 0 || !strings.Contains(strings.Join(e2.Warnings, "\n"), "opencode") {
		t.Fatalf("expected a warning naming the skipped opencode adapter, got %v", e2.Warnings)
	}
}

func TestDoctorReportsBuiltinSkillLinked(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	out := e.Doctor()
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, `skill "onto-open" linked (opencode)`) {
		t.Fatalf("doctor did not report the builtin skill as linked:\n%s", joined)
	}
}

func TestDoctorReportsLinkedCommand(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(commandTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var found bool
	for _, line := range e.Doctor() {
		if strings.Contains(line, "ok: command \"example-command\" linked (opencode)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor did not report example-command linked; got %v", e.Doctor())
	}
}

const subagentOpenCodeTOML = `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
`

func TestDoctorReportsLinkedSubagent(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(subagentOpenCodeTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	joined := strings.Join(e.Doctor(), "\n")
	if !strings.Contains(joined, `ok: subagent "onto-reviewer" linked (opencode)`) {
		t.Fatalf("doctor did not report onto-reviewer linked for opencode; got %s", joined)
	}
}

func TestStatusCleanAfterApply(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[settings.opencode]\nmodel=\"opus\"\n"), 0o644)

	e := buildStatusEngine(t, repo, home)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	drift, pending, err := buildStatusEngine(t, repo, home).Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 || pending != 0 {
		t.Fatalf("clean apply must yield no drift and no pending, got drift=%v pending=%d", drift, pending)
	}
}
