package host

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	s, err := NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s, root
}

func request(tool Tool, workflow workspacecfg.Workflow) InstallRequest {
	return InstallRequest{Tool: tool, Workflow: workflow, Binary: "homonto"}
}

// install plans and applies one tool's integration.
func install(t *testing.T, s *Service, req InstallRequest) Plan {
	t.Helper()
	plan, err := s.PlanInstall(t.Context(), req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if err := s.ApplyInstall(t.Context(), plan); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}
	return plan
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

func TestNewServiceRequiresAnAbsoluteRoot(t *testing.T) {
	if _, err := NewService("relative"); err == nil {
		t.Error("NewService(relative) = nil error, want rejection")
	}
	if _, err := NewService(""); err == nil {
		t.Error("NewService(\"\") = nil error, want rejection")
	}
}

func TestDetectReportsPresence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	targets, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("Detect returned %d targets, want one per supported tool", len(targets))
	}
	byTool := map[Tool]Target{}
	for _, tgt := range targets {
		byTool[tgt.Tool] = tgt
	}
	if !byTool[ToolClaude].Present {
		t.Error("claude was not detected despite its directory existing")
	}
	if byTool[ToolOpenCode].Present {
		t.Error("opencode was detected without its directory")
	}
	if _, err := Detect("relative"); err == nil {
		t.Error("Detect(relative) = nil error, want rejection")
	}
}

func TestPlanInstallsUsesDetectedTools(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("create Claude directory: %v", err)
	}

	plans, err := PlanInstalls(t.Context(), root, workspacecfg.WorkflowTask, InstallOptions{})
	if err != nil {
		t.Fatalf("PlanInstalls: %v", err)
	}
	if len(plans) != 1 || plans[0].Target.Tool != ToolClaude {
		t.Fatalf("planned tools = %+v, want Claude only", plans)
	}
}

// TestClaudeGetsOneSkillEntry pins the shape: one skill, one command that
// does nothing but invoke it, and the hooks. Two entry points carrying
// rules would be two places for a rule to hide.
func TestClaudeGetsOneSkillEntry(t *testing.T) {
	s, root := newService(t)
	install(t, s, request(ToolClaude, workspacecfg.WorkflowTask))

	skills, err := os.ReadDir(filepath.Join(root, ".claude", "skills"))
	if err != nil {
		t.Fatalf("read skills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name() != "homonto-task" {
		t.Fatalf("skills = %v, want exactly homonto-task", skills)
	}
	commands, err := os.ReadDir(filepath.Join(root, ".claude", "commands"))
	if err != nil {
		t.Fatalf("read commands: %v", err)
	}
	if len(commands) != 1 || commands[0].Name() != "homonto-task.md" {
		t.Fatalf("commands = %v, want exactly homonto-task.md", commands)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.json")); err != nil {
		t.Fatalf("settings.json was not written: %v", err)
	}
}

// TestOpenCodeGetsCommandSkillAndPlugin pins OpenCode's three pieces.
func TestOpenCodeGetsCommandSkillAndPlugin(t *testing.T) {
	s, root := newService(t)
	install(t, s, request(ToolOpenCode, workspacecfg.WorkflowChange))

	for _, rel := range []string{
		".opencode/skill/homonto-change/SKILL.md",
		".opencode/command/homonto-change.md",
		".opencode/plugin/homonto.js",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s was not installed: %v", rel, err)
		}
	}
}

// TestOnlyTheConfiguredWorkflowIsInstalled proves a Task workspace never
// grows a /homonto-change entry point, and vice versa.
func TestOnlyTheConfiguredWorkflowIsInstalled(t *testing.T) {
	for _, workflow := range []workspacecfg.Workflow{
		workspacecfg.WorkflowTask, workspacecfg.WorkflowChange,
	} {
		t.Run(string(workflow), func(t *testing.T) {
			s, root := newService(t)
			install(t, s, request(ToolClaude, workflow))
			want := "homonto-" + string(workflow)
			other := "homonto-task"
			if workflow == workspacecfg.WorkflowTask {
				other = "homonto-change"
			}
			if _, err := os.Stat(filepath.Join(root, ".claude", "skills", want)); err != nil {
				t.Errorf("%s was not installed: %v", want, err)
			}
			if _, err := os.Stat(filepath.Join(root, ".claude", "skills", other)); err == nil {
				t.Errorf("%s was installed in a %s workspace", other, workflow)
			}
		})
	}
}

// TestWrappersContainOnlyTheProtocolLoop is the load-bearing test of this
// package: a wrapper that learned a fifth verb has started to contain
// workflow policy, and thin integrations exist precisely so it cannot.
func TestWrappersContainOnlyTheProtocolLoop(t *testing.T) {
	for _, tool := range Tools() {
		t.Run(string(tool), func(t *testing.T) {
			s, root := newService(t)
			req := request(tool, workspacecfg.WorkflowTask)
			plan := install(t, s, req)
			total := 0
			for _, f := range plan.Files {
				if strings.HasSuffix(f.Path, "settings.json") {
					continue
				}
				body := read(t, root, f.Path)
				total += len(Invocations(f.Path, body, req.binary()))
				if line, ok := containsOnlyWrapperVerbs(f.Path, body, req.binary()); !ok {
					t.Errorf("%s invokes the binary outside the protocol loop: %q", f.Path, line)
				}
			}
			// A command file legitimately invokes nothing — its job is to
			// run the skill — but the integration as a whole must be able
			// to drive the binary, or it drives nothing.
			if total == 0 {
				t.Errorf("the %s integration invokes the binary nowhere", tool)
			}
		})
	}
	// And the guard is honest about what it is.
	s, root := newService(t)
	install(t, s, request(ToolOpenCode, workspacecfg.WorkflowTask))
	plugin := read(t, root, ".opencode/plugin/homonto.js")
	if !strings.Contains(plugin, "not a sandbox") {
		t.Error("the plugin does not say the write hook is a process gate rather than a sandbox")
	}
}

// TestSkillCarriesNoWorkflowPolicy proves the skill teaches the protocol
// and nothing about what the workflow requires.
func TestSkillCarriesNoWorkflowPolicy(t *testing.T) {
	s, root := newService(t)
	install(t, s, request(ToolClaude, workspacecfg.WorkflowTask))
	skill := read(t, root, ".claude/skills/homonto-task/SKILL.md")
	for _, forbidden := range []string{
		"plan_explore", "do_implement", "done_checks", "proposal.md", "design.md",
	} {
		if strings.Contains(skill, forbidden) {
			t.Errorf("the skill names the workflow internal %q; that rule belongs in the binary", forbidden)
		}
	}
	// It does teach the protocol.
	for _, want := range []string{"next --json", "accept-edit", "report", "decide", "blocked", "ready"} {
		if !strings.Contains(skill, want) {
			t.Errorf("the skill does not teach %q", want)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	s, _ := newService(t)
	req := request(ToolClaude, workspacecfg.WorkflowTask)
	install(t, s, req)

	plan, err := s.PlanInstall(t.Context(), req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if plan.Changes() {
		for _, f := range plan.Files {
			if f.Action.Writes() {
				t.Errorf("re-installing would %s %s", f.Action, f.Path)
			}
		}
		t.Fatalf("re-installing an unchanged integration would change something: %+v", plan.Ignore)
	}
}

// TestAHandEditedFileIsRefusedNotOverwritten is the ownership contract:
// an edit is a statement, and installation does not argue with it.
func TestAHandEditedFileIsRefusedNotOverwritten(t *testing.T) {
	s, root := newService(t)
	req := request(ToolClaude, workspacecfg.WorkflowTask)
	install(t, s, req)

	rel := ".claude/skills/homonto-task/SKILL.md"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	edited := read(t, root, rel) + "\n## My own notes\n\nKeep these.\n"
	if err := os.WriteFile(abs, []byte(edited), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	plan, err := s.PlanInstall(t.Context(), req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	conflicts := plan.Conflicts()
	if len(conflicts) != 1 || conflicts[0].Path != rel {
		t.Fatalf("conflicts = %+v, want the edited skill", conflicts)
	}
	if conflicts[0].Reason == "" {
		t.Error("the conflict does not explain itself")
	}
	// A plan with a conflict is refused WHOLE.
	if err := s.ApplyInstall(t.Context(), plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyInstall error = %v, want ErrConflict", err)
	}
	if read(t, root, rel) != edited {
		t.Fatal("the hand edit was overwritten")
	}
	// Adopting is explicit, and then it is replaced.
	req.Adopt = true
	install(t, s, req)
	if strings.Contains(read(t, root, rel), "My own notes") {
		t.Fatal("--adopt did not replace the edited file")
	}
}

// TestAStaleGeneratedFileIsUpdated proves ownership survives a version
// change: a file Homonto wrote in an older release is still Homonto's.
func TestAStaleGeneratedFileIsUpdated(t *testing.T) {
	s, root := newService(t)
	req := request(ToolClaude, workspacecfg.WorkflowTask)
	install(t, s, req)

	rel := ".claude/skills/homonto-task/SKILL.md"
	stale := Mark(rel, []byte("# An older release wrote this\n"))
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), stale, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := s.PlanInstall(t.Context(), req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	for _, f := range plan.Files {
		if f.Path != rel {
			continue
		}
		if f.Action != ActionUpdate {
			t.Fatalf("a stale generated file plans %s, want update", f.Action)
		}
	}
	if err := s.ApplyInstall(t.Context(), plan); err != nil {
		t.Fatalf("ApplyInstall: %v", err)
	}
	if strings.Contains(read(t, root, rel), "An older release") {
		t.Fatal("the stale file was not updated")
	}
}

// TestUserSettingsSurviveInstallation is the shared-document contract: a
// file Homonto shares is not one it may normalize.
func TestUserSettingsSurviveInstallation(t *testing.T) {
	s, root := newService(t)
	settings := map[string]any{
		"model":       "opus",
		"permissions": map[string]any{"allow": []any{"Bash(git status)"}},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": "my-own-tool --hello"}},
			}},
		},
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	install(t, s, request(ToolClaude, workspacecfg.WorkflowTask))

	var got map[string]any
	if err := json.Unmarshal([]byte(read(t, root, ".claude/settings.json")), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["model"] != "opus" {
		t.Errorf("the user's model choice was lost: %v", got["model"])
	}
	if got["permissions"] == nil {
		t.Error("the user's permissions were lost")
	}
	raw := read(t, root, ".claude/settings.json")
	if !strings.Contains(raw, "my-own-tool --hello") {
		t.Errorf("the user's own hook was removed:\n%s", raw)
	}
	for _, want := range []string{"homonto host probe", "homonto host guard"} {
		if !strings.Contains(raw, want) {
			t.Errorf("the settings do not install %q:\n%s", want, raw)
		}
	}

	// Re-installing does not duplicate Homonto's entries.
	install(t, s, request(ToolClaude, workspacecfg.WorkflowTask))
	raw = read(t, root, ".claude/settings.json")
	if strings.Count(raw, "homonto host probe") != 1 {
		t.Errorf("re-installing duplicated the probe hook:\n%s", raw)
	}
	if strings.Count(raw, "my-own-tool --hello") != 1 {
		t.Errorf("re-installing duplicated the user's hook:\n%s", raw)
	}
}

// TestUnparsableSettingsAreRefused proves Homonto does not rewrite a
// document it could not read.
func TestUnparsableSettingsAreRefused(t *testing.T) {
	s, root := newService(t)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	broken := "{ this is not json"
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := s.PlanInstall(t.Context(), request(ToolClaude, workspacecfg.WorkflowTask))
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if len(plan.Conflicts()) == 0 {
		t.Fatal("an unparsable settings file did not conflict")
	}
	if err := s.ApplyInstall(t.Context(), plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyInstall error = %v, want ErrConflict", err)
	}
	if read(t, root, ".claude/settings.json") != broken {
		t.Fatal("an unparsable settings file was rewritten")
	}
}

// TestGeneratedFilesAreIgnoredByDefault pins the spec's default, and the
// opt-in.
func TestGeneratedFilesAreIgnoredByDefault(t *testing.T) {
	s, root := newService(t)
	install(t, s, request(ToolClaude, workspacecfg.WorkflowTask))
	ignore := read(t, root, ".gitignore")
	if !strings.Contains(ignore, ".claude/") {
		t.Fatalf(".gitignore does not ignore the generated files:\n%s", ignore)
	}
	// Re-installing does not duplicate the entry.
	install(t, s, request(ToolClaude, workspacecfg.WorkflowTask))
	if strings.Count(read(t, root, ".gitignore"), ".claude/") != 1 {
		t.Errorf("re-installing duplicated the ignore entry:\n%s", read(t, root, ".gitignore"))
	}

	// Committing is an opt-in, never a side effect.
	other, otherRoot := newService(t)
	req := request(ToolOpenCode, workspacecfg.WorkflowTask)
	req.Commit = true
	install(t, other, req)
	if _, err := os.Stat(filepath.Join(otherRoot, ".gitignore")); err == nil {
		t.Error("--commit still wrote a .gitignore entry")
	}
}

func TestObserveReportsWhatIsInstalled(t *testing.T) {
	s, root := newService(t)
	req := request(ToolClaude, workspacecfg.WorkflowTask)
	install(t, s, req)
	target := Target{Tool: ToolClaude, Dir: ".claude", Present: true}

	obs, err := s.Observe(t.Context(), target, req)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.Healthy() {
		t.Fatalf("a fresh install is not healthy: %+v", obs)
	}
	if len(obs.Installed) != 2 {
		t.Fatalf("Installed = %v, want the skill and the command", obs.Installed)
	}

	// A hand edit is reported, not repaired.
	rel := ".claude/commands/homonto-task.md"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, []byte(read(t, root, rel)+"\nedited\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	obs, err = s.Observe(t.Context(), target, req)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs.Modified) != 1 || obs.Modified[0] != rel {
		t.Fatalf("Modified = %v, want the edited command", obs.Modified)
	}
	if obs.Healthy() {
		t.Fatal("an integration with a modified file reported healthy")
	}

	// A file something else wrote is foreign, not modified.
	if err := os.WriteFile(abs, []byte("# written by another tool\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	obs, err = s.Observe(t.Context(), target, req)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs.Foreign) != 1 || obs.Foreign[0] != rel {
		t.Fatalf("Foreign = %v, want the unmarked file", obs.Foreign)
	}

	// A removed file is missing.
	if err := os.Remove(abs); err != nil {
		t.Fatalf("remove: %v", err)
	}
	obs, err = s.Observe(t.Context(), target, req)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs.Missing) != 1 || obs.Missing[0] != rel {
		t.Fatalf("Missing = %v, want the removed command", obs.Missing)
	}
}

func TestInstallRequestValidation(t *testing.T) {
	for _, req := range []InstallRequest{
		{Tool: "notepad", Workflow: workspacecfg.WorkflowTask},
		{Tool: ToolClaude, Workflow: "sideways"},
		{Tool: ToolClaude, Workflow: workspacecfg.WorkflowTask, Binary: "my homonto"},
	} {
		if err := req.Validate(); err == nil {
			t.Errorf("an invalid request was accepted: %+v", req)
		}
	}
	s, _ := newService(t)
	if _, err := s.PlanInstall(t.Context(), InstallRequest{}); err == nil {
		t.Error("PlanInstall accepted an empty request")
	}
}

func TestOwnershipMarker(t *testing.T) {
	body := []byte("# Hello\n\nBody.\n")
	marked := Mark("x.md", body)
	if !strings.HasPrefix(string(marked), "<!-- homonto-managed: sha256=") {
		t.Fatalf("the markdown marker is not a markdown comment: %q", marked)
	}
	if !Owned("x.md", marked) {
		t.Fatal("a freshly marked file is not owned")
	}
	if !strings.HasSuffix(string(marked), string(body)) {
		t.Fatal("marking changed the body")
	}
	if Owned("x.md", append(marked, []byte("edited\n")...)) {
		t.Fatal("an edited file is still owned")
	}
	if Owned("x.md", body) {
		t.Fatal("an unmarked file is owned")
	}
	js := Mark("p.js", []byte("export const x = 1;\n"))
	if !strings.HasPrefix(string(js), "// homonto-managed: sha256=") {
		t.Fatalf("the javascript marker is not a javascript comment: %q", js)
	}
	if !Owned("p.js", js) {
		t.Fatal("a marked javascript file is not owned")
	}
}

// TestBinaryIsConfigurable proves an install can point at a specific
// binary, which is what a self-update leaves behind.
func TestBinaryIsConfigurable(t *testing.T) {
	s, root := newService(t)
	req := request(ToolOpenCode, workspacecfg.WorkflowTask)
	req.Binary = "/usr/local/bin/homonto"
	install(t, s, req)
	plugin := read(t, root, ".opencode/plugin/homonto.js")
	if !strings.Contains(plugin, `"/usr/local/bin/homonto"`) {
		t.Fatalf("the plugin does not invoke the configured binary:\n%s", plugin)
	}
	skill := read(t, root, ".opencode/skill/homonto-task/SKILL.md")
	if !strings.Contains(skill, "/usr/local/bin/homonto next --json") {
		t.Fatalf("the skill does not invoke the configured binary:\n%s", skill)
	}
}
