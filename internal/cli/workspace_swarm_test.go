package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/swarm"
	"github.com/spf13/pflag"
)

func TestWorkspaceSwarmCLI(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "source")
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(repo, "init", "-b", "main")
	gitIn(repo, "config", "user.name", "Swarm Test")
	gitIn(repo, "config", "user.email", "swarm@example.invalid")
	gitIn(repo, "commit", "--allow-empty", "-m", "base")
	path := filepath.Join(root, "homonto.toml")
	text := `schema_version=2
[repos]
app='source'
[worktrees]
dir='allocated'
[engines.no]
source='builtin:no'
scope='project'
[subagents.no-implementer.opencode]
model='test/fast'
[subagents.no-reviewer.opencode]
model='test/careful'
[integration.has-answer]
repos=['app']
repo='app'
cwd='.'
command=['sh','-c','grep -q 42 answer']
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(root, "brief.md")
	if err := os.WriteFile(brief, []byte("Write the answer.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string, error) {
		t.Helper()
		cmd := NewRootCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"workspace", "swarm"}, append(args, "--config", path)...))
		err := cmd.Execute()
		return stdout.String(), stderr.String(), err
	}
	if _, _, err := run("create", "demo", "--repo", "app", "--brief", brief, "--candidate", "a=no-implementer", "--candidate", "b=nobody"); err == nil || !strings.Contains(err.Error(), "routed model") {
		t.Fatalf("an unknown agent was accepted: %v", err)
	}
	out, _, err := run("create", "demo", "--repo", "app", "--brief", brief, "--candidate", "a=no-implementer", "--candidate", "b=no-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	var sw swarm.Swarm
	if err := json.Unmarshal([]byte(out), &sw); err != nil || len(sw.Candidates) != 2 || sw.Candidates[0].Models["opencode"] != "test/fast" || sw.Candidates[1].Models["opencode"] != "test/careful" {
		t.Fatalf("create JSON = %s, %v", out, err)
	}
	for _, c := range []struct{ name, answer string }{{"a", "42"}, {"b", "41"}} {
		dir := filepath.Join(root, "allocated", "app", "workspace-"+swarm.TaskName("demo", c.name))
		if err := os.WriteFile(filepath.Join(dir, "answer"), []byte(c.answer+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(dir, "add", "answer")
		gitIn(dir, "commit", "-m", "answer "+c.answer)
	}
	if _, _, err := run("check", "demo"); err == nil {
		t.Fatal("check ran without paused writers")
	}
	if _, _, err := run("check", "demo", "--writers-paused"); err != nil {
		t.Fatalf("a failing candidate failed the check command: %v", err)
	}
	out, stderr, err := run("compare", "demo", "--html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"test/fast", "test/careful", "check has-answer", "passed", "failed", "verified"} {
		if !strings.Contains(out, want) {
			t.Errorf("compare lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr, "compare.html") {
		t.Errorf("compare --html did not name its page: %s", stderr)
	}
	if _, _, err := run("select", "demo", "--candidate", "b"); err == nil {
		t.Fatal("selected the failing candidate")
	}
	if _, _, err := run("select", "demo", "--candidate", "a"); err != nil {
		t.Fatal(err)
	}
	gitIn(repo, "merge", "--ff-only", "work-demo--a/app")
	if _, _, err := run("cleanup", "demo"); err == nil {
		t.Fatal("cleanup without --yes")
	}
	out, _, err = run("cleanup", "demo", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var report swarm.CleanupReport
	if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.Removed) != 2 || len(report.Deleted) != 1 {
		t.Fatalf("cleanup = %s, %v", out, err)
	}
	if branches := gitIn(repo, "branch", "--format=%(refname:short)"); branches != "main" {
		t.Fatalf("branches left = %q", branches)
	}
}

// The swarm skill and command drive the binary; every subcommand and flag
// they name must exist.
func TestSwarmSkillNamesRealCommands(t *testing.T) {
	re := regexp.MustCompile("homonto workspace swarm ([a-z]+)(?: (start|stop|reap))?")
	flag := regexp.MustCompile(`--[a-z][a-z-]+`)
	for _, file := range []string{"../../catalog/skills/swarm/SKILL.md", "../../catalog/commands/swarm.md"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		flags := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			args := []string{"workspace", "swarm", m[1]}
			if m[2] != "" {
				args = append(args, m[2])
			}
			cmd, rest, err := NewRootCmd().Find(args)
			if err != nil || len(rest) != 0 || cmd.Name() != args[len(args)-1] {
				t.Errorf("%s names %q, which is not a command", file, m[0])
				continue
			}
			cmd.Flags().VisitAll(func(f *pflag.Flag) { flags["--"+f.Name] = true })
			cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) { flags["--"+f.Name] = true })
		}
		for _, f := range flag.FindAllString(string(data), -1) {
			if !flags[f] {
				t.Errorf("%s names flag %s, which no swarm command takes", file, f)
			}
		}
	}
}
