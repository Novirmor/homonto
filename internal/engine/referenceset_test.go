package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/workflowroot"
	"github.com/Novirmor/homonto/internal/workflowstatus"
	"github.com/Novirmor/homonto/internal/worktask"
)

func referencePackage(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "reference-set", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestReferenceSetRunsFromRemoteOnBothHarnesses consumes the reference
// engine, workflow and service as digest-pinned remote: archives — no
// homonto source names them — projects them onto OpenCode and Claude Code,
// and drives the engine's own binary through a workspace task's start,
// resume, integration check and release (M4.8).
func TestReferenceSetRunsFromRemoteOnBothHarnesses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	archives := t.TempDir()
	source := map[string]string{}
	for _, name := range []string{"ticket-engine", "ticket-workflow", "ticket-service"} {
		tarball := packDir(t, referencePackage(t, name), filepath.Join(archives, name+".tar.gz"))
		source[name] = "source = \"remote:file://" + tarball + "\"\ndigest = \"" + pinFor(t, tarball).String() + "\"\nscope = \"project\"\n"
	}
	home, root := t.TempDir(), t.TempDir()
	api := filepath.Join(root, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, api, "init", "-q", "-b", "main")
	gitRun(t, api, "commit", "-q", "--allow-empty", "-m", "base")
	base := gitRun(t, api, "rev-parse", "HEAD")
	cfg := `schema_version = 2
harnesses = ["opencode", "claude"]

[repos]
api = "./api"

[workflow]
root = "records"

[worktrees]
dir = "./worktrees"

[integration.unit]
repos = ["api"]
repo = "api"
cwd = "."
command = ["git", "status", "--short"]

[engines.ticket-engine]
` + source["ticket-engine"] + `
[workflows.ticket-workflow]
` + source["ticket-workflow"] + `
[services.ticket-service]
` + source["ticket-service"] + `
[workflows.ticket-workflow.processes.bug]
steps = ["reproduce", "fix", "review"]

[routing.default.opencode]
model = "test/model"

[routing.default.claude]
model = "sonnet"
`
	configPath := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), configPath, home, "content")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ticket-engine", "ticket-workflow", "ticket-service"} {
		if r, err := cl.Conformance(name); err != nil || !r.Passed() {
			t.Fatalf("%s conformance: %+v %v", name, r, err)
		}
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		".opencode/skills/ticket-to-delivery/SKILL.md", ".claude/skills/ticket-to-delivery/SKILL.md",
		".opencode/command/ticket.md", ".claude/commands/ticket.md",
		".opencode/agent/ticket-reviewer.md", ".claude/agents/ticket-reviewer.md",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("%s not projected: %v", path, err)
		}
	}
	implementer, err := os.ReadFile(filepath.Join(root, ".claude/agents/ticket-implementer.md"))
	if err != nil || !strings.Contains(string(implementer), "homonto_policy") {
		t.Errorf("claude implementer lacks its policy: %v", err)
	}
	coordinator, err := os.ReadFile(filepath.Join(root, ".opencode/agent/homonto.md"))
	if err != nil || !strings.Contains(string(coordinator), `"ticket-cli comment*": ask`) {
		t.Errorf("coordinator lacks the ticket service's asks: %v", err)
	}

	// The engine's own binary starts the change; homonto only reads it.
	if err := workflowroot.WriteLayoutMarker(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: e.ConfigPath, WorkflowRoot: e.WorkspaceLayout.WorkflowRoot, GitMode: e.WorkspaceLayout.GitMode}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(referencePackage(t, "ticket-engine"), "bin", "ticket-engine")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("ticket-engine %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	records := e.WorkspaceLayout.WorkflowRoot
	id := run("start", "abc-12", "--root", records, "--repo", "api", "--base", base, "--branch", "main")
	run("phase", "abc-12", "--root", records, "implement")

	s, err := worktask.OpenConfig(e.Cfg, e.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Create(context.Background(), "abc-12", "ticket-engine", "abc-12", "worktree", []string{"api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.NativeID != "id:"+id || task.Checkouts["api"].Base != base {
		t.Fatalf("task binding: native %q, %+v", task.NativeID, task.Checkouts["api"])
	}
	// Resume after an interruption: a fresh store, the same task.
	s2, err := worktask.OpenConfig(e.Cfg, e.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := s2.Load(context.Background(), "abc-12")
	if err != nil || resumed.NativeID != task.NativeID {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	if _, err := s2.RunCheck(context.Background(), resumed, "unit", nil); err != nil {
		t.Fatalf("integration check: %v", err)
	}
	// The snapshot every harness injects names the engine's change.
	snap := workflowstatus.ReadConfig(e.ConfigPath)
	found := false
	for _, c := range snap.Changes {
		found = found || (c.Workflow == "ticket-engine" && c.Name == "abc-12" && c.Phase == "implement" && c.Status == "active")
	}
	if !found {
		t.Fatalf("snapshot lacks the engine's change: %+v", snap)
	}
	run("finish", "abc-12", "--root", records)
	if _, err := s2.Cleanup(context.Background(), "abc-12", true); err != nil {
		t.Fatalf("release: %v", err)
	}
}
