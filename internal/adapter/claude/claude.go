// Package claude is the Claude Code harness adapter (M4.4): native projection
// of skills, commands and agents into `.claude/`, user settings into
// ~/.claude/settings.json, and the workflow runtime integration — hooks,
// permission rules and the homonto MCP server — into the project's
// `.claude/settings.json` and `.mcp.json`. It runs on the shared projection
// control flow (ADR 0014) and renders nothing it cannot express natively
// (ADR 0082).
package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Novirmor/homonto/internal/adapter"
	"github.com/Novirmor/homonto/internal/adapter/baseadapter"
	"github.com/Novirmor/homonto/internal/adapter/copyproj"
	"github.com/Novirmor/homonto/internal/adapter/fileproj"
	"github.com/Novirmor/homonto/internal/adapter/jsoncodec"
	"github.com/Novirmor/homonto/internal/adapter/structproj"
	"github.com/Novirmor/homonto/internal/capability"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/copyfile"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/Novirmor/homonto/internal/jsonutil"
	"github.com/Novirmor/homonto/internal/secret"
	"github.com/Novirmor/homonto/internal/state"
)

// Tool is the adapter's state partition and target name.
const Tool = "claude"

// Workflow binds the runtime integration: the selected config, the hosting
// providers whose draft tools the core serves, and the coordinator agent the
// session runs as.
type Workflow struct {
	ConfigPath  string
	Hosts       []string
	Coordinator string
}

// Adapter projects desired config into Claude Code's files.
type Adapter struct {
	baseadapter.Base
	workflow *Workflow
	// policyHook installs the PreToolUse hook that enforces rendered agent
	// policies, even without the workflow integration.
	policyHook bool
}

// Capabilities declares what Claude Code provides (ADR 0097, ADR 0105).
func (a *Adapter) Capabilities() capability.Declaration { return capability.ClaudeCode() }

// New builds a Claude Code adapter. home is $HOME; content holds owned skills.
func New(home, content string) *Adapter {
	return &Adapter{Base: baseadapter.Base{Tool: Tool, VariantSuffix: ".claude.md", Home: home, Content: content}}
}

func (a *Adapter) WithProjectRoot(p string) *Adapter         { a.ProjectRoot = p; return a }
func (a *Adapter) WithCatalogRoot(p string) *Adapter         { a.CatalogRoot = p; return a }
func (a *Adapter) WithCommandCatalogRoot(p string) *Adapter  { a.CommandCatalogRoot = p; return a }
func (a *Adapter) WithSubagentCatalogRoot(p string) *Adapter { a.SubagentCatalogRoot = p; return a }
func (a *Adapter) WithRemoteSubagentRoot(p string) *Adapter  { a.RemoteSubagentRoot = p; return a }

// WithRepo puts the adapter in repo mode (ADR 0024): only repo-tagged
// project resources, no user settings and no workflow integration.
func (a *Adapter) WithRepo(repo string) *Adapter { a.RepoName = repo; return a }

// WithWorkflow enables the runtime integration in the project files.
func (a *Adapter) WithWorkflow(w *Workflow) *Adapter { a.workflow = w; return a }

// WithPolicyHook installs the agent-policy hook.
func (a *Adapter) WithPolicyHook(on bool) *Adapter { a.policyHook = on; return a }

func (a *Adapter) settingsFile() string { return filepath.Join(a.Home, ".claude", "settings.json") }
func (a *Adapter) projectSettingsFile() string {
	return filepath.Join(a.ProjectRoot, ".claude", "settings.json")
}
func (a *Adapter) projectMCPFile() string { return filepath.Join(a.ProjectRoot, ".mcp.json") }

func readDoc(path string) ([]byte, error) { return baseadapter.ReadStandardizedJSON(path) }

func (a *Adapter) readProject(path func() string) ([]byte, error) {
	if a.ProjectRoot == "" {
		return jsonutil.Standardize(nil)
	}
	return readDoc(path())
}

func settingDocPath(key string) string {
	return jsonutil.EscapePath(strings.TrimPrefix(key, "setting."))
}

// Project keys are homonto's own fixed names; their dots are path separators.
func projSettingDocPath(key string) string { return strings.TrimPrefix(key, "projsetting.") }
func projMCPDocPath(key string) string {
	return "mcpServers." + jsonutil.EscapePath(strings.TrimPrefix(key, "projmcp."))
}

func (a *Adapter) desiredSettings(c *config.Config) map[string]string {
	out := map[string]string{}
	if a.RepoName != "" {
		return out
	}
	for k, v := range c.Settings.Claude {
		out["setting."+k] = structproj.MustJSON(v)
	}
	return out
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// hookCommand is the one command every homonto hook runs.
func (a *Adapter) hookCommand() string {
	parts := []string{"homonto", "runtime", "hook"}
	if a.workflow != nil {
		parts = append(parts, "--config", shellQuote(a.workflow.ConfigPath))
	} else {
		parts = append(parts, "--config", shellQuote(filepath.Join(a.ProjectRoot, "homonto.toml")))
	}
	for _, dir := range []string{a.SubagentsDir("project"), a.SubagentsDir("user")} {
		if dir != "" {
			parts = append(parts, "--agents-dir", shellQuote(dir))
		}
	}
	if a.workflow != nil && a.workflow.Coordinator != "" {
		parts = append(parts, "--coordinator", shellQuote(a.workflow.Coordinator))
	}
	return strings.Join(parts, " ")
}

// PolicyMatcher names the tools whose calls the agent-policy hook judges.
const PolicyMatcher = "Bash|Read|Edit|Write|MultiEdit|NotebookEdit|Agent|Task|AskUserQuestion|mcp__homonto__.*"

// workflowEvents are the hook events the runtime integration observes
// (harness-architecture.md § Runtime; ADR 0103).
var workflowEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "PermissionDenied", "SubagentStop", "SessionEnd"}

func hookGroup(matcher, command string) []any {
	group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}
	if matcher != "" {
		group["matcher"] = matcher
	}
	return []any{group}
}

// coordinatorTools are the runtime tools the coordinator may call: reads are
// allowed outright, publication always asks (the native permission prompt
// precedes the correlated approval question).
func (w *Workflow) coordinatorTools() (allow, ask []string) {
	allow = []string{"mcp__homonto__homonto_status", "mcp__homonto__homonto_handoff"}
	for _, h := range w.Hosts {
		allow = append(allow, "mcp__homonto__homonto_"+h+"_draft", "mcp__homonto__homonto_"+h+"_status")
		ask = append(ask, "mcp__homonto__homonto_"+h+"_publish")
	}
	return allow, ask
}

func (a *Adapter) desiredProjectSettings() map[string]string {
	out := map[string]string{}
	if a.ProjectRoot == "" || a.RepoName != "" || (a.workflow == nil && !a.policyHook) {
		return out
	}
	command := a.hookCommand()
	out["projsetting.hooks.PreToolUse"] = structproj.MustJSON(hookGroup(PolicyMatcher, command))
	if a.workflow == nil {
		return out
	}
	for _, event := range workflowEvents {
		out["projsetting.hooks."+event] = structproj.MustJSON(hookGroup("", command))
	}
	allow, ask := a.workflow.coordinatorTools()
	out["projsetting.permissions.allow"] = structproj.MustJSON(allow)
	if len(ask) > 0 {
		out["projsetting.permissions.ask"] = structproj.MustJSON(ask)
	}
	// The hook relay and the core are the harness's, never the model's.
	out["projsetting.permissions.deny"] = structproj.MustJSON([]string{"Bash(homonto runtime:*)"})
	out["projsetting.enabledMcpjsonServers"] = structproj.MustJSON([]string{"homonto"})
	if a.workflow.Coordinator != "" {
		out["projsetting.agent"] = structproj.MustJSON(a.workflow.Coordinator)
	}
	return out
}

func (a *Adapter) desiredProjectMCP() map[string]string {
	out := map[string]string{}
	if a.ProjectRoot == "" || a.RepoName != "" || a.workflow == nil {
		return out
	}
	args := []string{"runtime", "mcp", "--harness", "claude", "--config", a.workflow.ConfigPath}
	for _, h := range a.workflow.Hosts {
		args = append(args, "--host", h)
	}
	out["projmcp.homonto"] = structproj.MustJSON(map[string]any{"type": "stdio", "command": "homonto", "args": args})
	return out
}

// RuntimeServer is the one .mcp.json server name homonto owns (ADR 0107).
const RuntimeServer = "homonto"

// RuntimeFindings reports the runtime MCP registration for doctor (ADR 0107).
// It reads only homonto's own server entry: the user's servers in the same
// file are never enumerated, compared or reported.
func (a *Adapter) RuntimeFindings(lookPath func(string) (string, error)) []string {
	desired := a.desiredProjectMCP()
	want, ok := desired["projmcp."+RuntimeServer]
	if !ok {
		return nil
	}
	var out []string
	path := a.projectMCPFile()
	doc, err := readDoc(path)
	if err != nil {
		return []string{fmt.Sprintf("warn: claude: runtime MCP server: cannot read %s: %v", path, err)}
	}
	codec := jsoncodec.Codec{}
	got, present, err := codec.Get(doc, projMCPDocPath("projmcp."+RuntimeServer))
	switch {
	case err != nil:
		out = append(out, fmt.Sprintf("warn: claude: runtime MCP server: %s: %v", path, err))
	case !present:
		out = append(out, fmt.Sprintf("warn: claude: runtime MCP server %q is not registered in %s (run `homonto apply`)", RuntimeServer, path))
	case got != codec.Canonical(want):
		out = append(out, fmt.Sprintf("warn: claude: runtime MCP server %q in %s differs from the selected config (run `homonto apply`)", RuntimeServer, path))
	default:
		out = append(out, fmt.Sprintf("ok: claude: runtime MCP server %q registered in %s", RuntimeServer, path))
	}
	if _, err := lookPath("homonto"); err != nil {
		out = append(out, "warn: claude: `homonto` is not on PATH; Claude Code cannot start the runtime MCP server or its hooks")
	}
	return out
}

type namespace struct {
	prefix  string
	desired map[string]string
	doc     []byte
	path    structproj.PathFor
	file    func() string
}

func (a *Adapter) namespaces(c *config.Config) ([]*namespace, error) {
	user := []byte(nil)
	var err error
	if a.RepoName == "" {
		if user, err = readDoc(a.settingsFile()); err != nil {
			return nil, err
		}
	} else if user, err = jsonutil.Standardize(nil); err != nil {
		return nil, err
	}
	proj, err := a.readProject(a.projectSettingsFile)
	if err != nil {
		return nil, err
	}
	mcp, err := a.readProject(a.projectMCPFile)
	if err != nil {
		return nil, err
	}
	return []*namespace{
		{"setting.", a.desiredSettings(c), user, settingDocPath, a.settingsFile},
		{"projsetting.", a.desiredProjectSettings(), proj, projSettingDocPath, a.projectSettingsFile},
		{"projmcp.", a.desiredProjectMCP(), mcp, projMCPDocPath, a.projectMCPFile},
	}, nil
}

func managedPrefix(k string) bool {
	return baseadapter.HasAnyPrefix(k, "skill.", "command.", "subagent.")
}

// Plan diffs the desired Claude Code files against disk and state.
func (a *Adapter) Plan(c *config.Config, st *state.State) (adapter.ChangeSet, error) {
	if err := a.Expand(c); err != nil {
		return adapter.ChangeSet{}, err
	}
	cs := adapter.ChangeSet{Tool: a.Name()}
	spaces, err := a.namespaces(c)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	codec := jsoncodec.Codec{}
	for _, ns := range spaces {
		changes, err := structproj.Project(a.Name(), ns.prefix, ns.desired, ns.doc, st, codec, ns.path)
		if err != nil {
			return adapter.ChangeSet{}, err
		}
		cs.Changes = append(cs.Changes, changes...)
	}
	roots := a.ManagedRoots()
	for _, links := range [][]fileproj.Link{a.SkillFileLinks(), a.CommandFileLinks(), a.SubagentFileLinks()} {
		changes, err := fileproj.Project(a.Name(), links, st, roots)
		if err != nil {
			return adapter.ChangeSet{}, err
		}
		cs.Changes = append(cs.Changes, changes...)
	}
	declared := map[string]bool{}
	for _, e := range a.Skills {
		declared["skill."+e.Name] = true
	}
	for _, e := range a.Commands {
		declared["command."+e.Name] = true
	}
	for _, e := range a.Subagents {
		declared["subagent."+e.Name] = true
	}
	copyOps, err := a.PlanCopyOps(st)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	for _, op := range copyOps {
		name := copyproj.Name(op.Dst)
		switch op.Action {
		case copyfile.Conflict:
			return adapter.ChangeSet{}, fmt.Errorf("claude: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		case copyfile.Create:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "create", Key: "subagentcopy." + name, New: op.Dst, Cause: adapter.CauseDeclare})
		case copyfile.Update, copyfile.LocalEdit:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "update", Key: "subagentcopy." + name, New: op.Dst, Cause: adapter.CauseUpdate})
		case copyfile.Prune:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: "subagentcopy." + name, Old: op.Dst, Cause: adapter.CauseRemove})
		}
	}
	for _, k := range st.Keys(a.Name()) {
		if declared[k] || !managedPrefix(k) {
			continue
		}
		cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: k, Old: adapter.SecretRedaction, Cause: adapter.CauseRemove})
	}
	sort.SliceStable(cs.Changes, func(i, j int) bool { return cs.Changes[i].Key < cs.Changes[j].Key })
	return cs, nil
}

// ObserveHashes re-hashes every recorded key still on disk.
func (a *Adapter) ObserveHashes(st *state.State) (map[string]string, error) {
	out := map[string]string{}
	codec := jsoncodec.Codec{}
	type obs struct {
		prefix string
		file   func() string
		path   structproj.PathFor
		global bool
	}
	for _, o := range []obs{
		{"setting.", a.settingsFile, settingDocPath, true},
		{"projsetting.", a.projectSettingsFile, projSettingDocPath, false},
		{"projmcp.", a.projectMCPFile, projMCPDocPath, false},
	} {
		if (o.global && a.RepoName != "") || (!o.global && a.ProjectRoot == "") {
			continue
		}
		doc, err := readDoc(o.file())
		if err != nil {
			return nil, err
		}
		got, err := structproj.Observe(a.Name(), o.prefix, doc, st, codec, o.path)
		if err != nil {
			return nil, err
		}
		for k, v := range got {
			out[k] = v
		}
	}
	for _, prefix := range []string{"skill.", "command.", "subagent."} {
		for k, v := range fileproj.Observe(a.Name(), prefix, st) {
			out[k] = v
		}
	}
	for _, key := range st.Keys(a.Name()) {
		if !strings.HasPrefix(key, "subagentcopy.") {
			continue
		}
		e, _ := st.Get(a.Name(), key)
		if content, err := os.ReadFile(e.Desired); err == nil {
			out[key] = copyfile.Hash(content)
		}
	}
	return out, nil
}

// Apply writes the planned changes.
func (a *Adapter) Apply(cfg *config.Config, cs adapter.ChangeSet, res *secret.Resolver, st *state.State) error {
	if err := a.Expand(cfg); err != nil {
		return err
	}
	spaces, err := a.namespaces(cfg)
	if err != nil {
		return err
	}
	roots := a.ManagedRoots()
	// Fail on any link or copy conflict before writing a single file.
	for _, links := range [][]fileproj.Link{a.SkillFileLinks(), a.CommandFileLinks(), a.SubagentFileLinks()} {
		if err := fileproj.Conflicts(a.Name(), links, st, roots); err != nil {
			return err
		}
	}
	copyOps, err := a.PlanCopyOps(st)
	if err != nil {
		return err
	}
	for _, op := range copyOps {
		if op.Action == copyfile.Conflict {
			return fmt.Errorf("claude: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		}
	}
	codec := jsoncodec.Codec{}
	for _, ns := range spaces {
		doc, changed, err := structproj.Apply(a.Name(), ns.prefix, baseadapter.FilterChanges(cs.Changes, ns.prefix), ns.doc, codec, res, st, ns.path)
		if err != nil {
			return err
		}
		if changed {
			if err := os.MkdirAll(filepath.Dir(ns.file()), 0o755); err != nil {
				return err
			}
			if err := fsutil.WriteAtomic(ns.file(), doc); err != nil {
				return err
			}
		}
	}
	dsts := func(dir func(string) string, suffix string) func(string) []string {
		return func(k string) []string {
			_, name, _ := strings.Cut(k, ".")
			return []string{filepath.Join(dir("user"), name+suffix), filepath.Join(dir("project"), name+suffix)}
		}
	}
	for _, ns := range []struct {
		prefix string
		dsts   func(string) []string
	}{
		{"skill.", dsts(a.SkillsDir, "")},
		{"command.", dsts(a.CommandsDir, ".md")},
		{"subagent.", dsts(a.SubagentsDir, ".md")},
	} {
		if err := fileproj.ApplyState(a.Name(), baseadapter.FilterChanges(cs.Changes, ns.prefix), st, roots, ns.dsts); err != nil {
			return err
		}
	}
	for _, links := range [][]fileproj.Link{a.SkillFileLinks(), a.CommandFileLinks(), a.SubagentFileLinks()} {
		if err := fileproj.ApplyLinks(a.Name(), links, st, roots); err != nil {
			return err
		}
	}
	return a.ApplyCopySubagents(st)
}

// Describe enumerates the keys this adapter projects, for explain.
func (a *Adapter) Describe(c *config.Config) []adapter.ManagedResource {
	if err := a.Expand(c); err != nil {
		return nil
	}
	var out []adapter.ManagedResource
	add := func(kind, name, key, dst string) {
		out = append(out, adapter.ManagedResource{Tool: a.Name(), Key: key, Kind: kind, Name: name, Destination: dst,
			Origin: &state.Origin{Kind: "direct"}})
	}
	for k := range a.desiredSettings(c) {
		add("setting", strings.TrimPrefix(k, "setting."), k, a.settingsFile())
	}
	for k := range a.desiredProjectSettings() {
		add("projsetting", strings.TrimPrefix(k, "projsetting."), k, a.projectSettingsFile())
	}
	for k := range a.desiredProjectMCP() {
		add("projmcp", strings.TrimPrefix(k, "projmcp."), k, a.projectMCPFile())
	}
	for kind, links := range map[string][]fileproj.Link{"skill": a.SkillFileLinks(), "command": a.CommandFileLinks(), "subagent": a.SubagentFileLinks()} {
		for _, l := range links {
			_, name, _ := strings.Cut(l.Key, ".")
			add(kind, name, l.Key, l.Dst)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
