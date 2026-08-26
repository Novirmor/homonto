package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// ErrConflict reports a plan that would overwrite a file Homonto does not
// own.
var ErrConflict = errors.New("host: a generated file was modified by hand")

// Service installs host integrations into one control repository.
type Service struct {
	root string
}

// NewService binds an installer to the absolute control repository root.
func NewService(root string) (*Service, error) {
	if root == "" {
		return nil, fmt.Errorf("host: control root must not be empty")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("host: control root %q must be an absolute path", root)
	}
	return &Service{root: root}, nil
}

// Detect reports which host tools are in use in a control repository.
//
// Presence is the tool's own project-local directory existing. That is a
// deliberately weak signal, and the right one: Homonto does not install
// itself into a tool nobody uses here, and does not refuse to install into
// one just because the user has not opened it yet.
func Detect(root string) ([]Target, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("host: control root %q must be an absolute path", root)
	}
	out := make([]Target, 0, len(Tools()))
	for _, tool := range Tools() {
		dir, err := tool.Dir()
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(filepath.Join(root, dir))
		out = append(out, Target{Tool: tool, Dir: dir, Present: err == nil && info.IsDir()})
	}
	return out, nil
}

// PlanInstalls works out what installing the requested host tools would do.
func PlanInstalls(ctx context.Context, controlRoot string, workflow workspacecfg.Workflow, opts InstallOptions) ([]Plan, error) {
	service, err := NewService(controlRoot)
	if err != nil {
		return nil, err
	}
	tools, err := installTools(controlRoot, opts.Tools)
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(tools))
	for _, tool := range tools {
		plan, err := service.PlanInstall(ctx, InstallRequest{
			Tool: tool, Workflow: workflow, Binary: opts.Binary, Adopt: opts.Adopt, Commit: opts.Commit,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// ApplyInstalls writes the host-install plans into the control repository.
func ApplyInstalls(ctx context.Context, controlRoot string, plans []Plan) error {
	service, err := NewService(controlRoot)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if err := service.ApplyInstall(ctx, plan); err != nil {
			return err
		}
	}
	return nil
}

// ObserveInstalls reports the integrations installed for every detected tool.
func ObserveInstalls(ctx context.Context, controlRoot string, workflow workspacecfg.Workflow, binary string) ([]Observation, error) {
	service, err := NewService(controlRoot)
	if err != nil {
		return nil, err
	}
	targets, err := Detect(controlRoot)
	if err != nil {
		return nil, err
	}
	var out []Observation
	for _, target := range targets {
		if !target.Present {
			continue
		}
		obs, err := service.Observe(ctx, target, InstallRequest{
			Tool: target.Tool, Workflow: workflow, Binary: binary,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, obs)
	}
	return out, nil
}

// installTools resolves which tools an install should target.
func installTools(controlRoot string, requested []string) ([]Tool, error) {
	if len(requested) > 0 {
		out := make([]Tool, 0, len(requested))
		for _, name := range requested {
			tool := Tool(name)
			if !tool.Known() {
				return nil, fmt.Errorf("app: %q is not a supported host tool", name)
			}
			out = append(out, tool)
		}
		return out, nil
	}
	targets, err := Detect(controlRoot)
	if err != nil {
		return nil, err
	}
	var out []Tool
	for _, target := range targets {
		if target.Present {
			out = append(out, target.Tool)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"app: no host tool is in use here; name one with --tool (%s or %s)",
			ToolClaude, ToolOpenCode)
	}
	return out, nil
}

// PlanInstall works out what installing one tool's integration would do,
// without doing any of it.
func (s *Service) PlanInstall(ctx context.Context, req InstallRequest) (Plan, error) {
	if err := req.Validate(); err != nil {
		return Plan{}, err
	}
	dir, err := req.Tool.Dir()
	if err != nil {
		return Plan{}, err
	}
	info, statErr := os.Stat(filepath.Join(s.root, dir))
	plan := Plan{Target: Target{
		Tool: req.Tool, Dir: dir, Present: statErr == nil && info.IsDir(),
	}}

	files, err := generatedFiles(req)
	if err != nil {
		return Plan{}, err
	}
	for _, f := range files {
		planned, err := s.planFile(f, req.Adopt)
		if err != nil {
			return Plan{}, err
		}
		plan.Files = append(plan.Files, planned)
	}
	if settings, ok := settingsPath(req.Tool); ok {
		planned, err := s.planSettings(settings, req)
		if err != nil {
			return Plan{}, err
		}
		plan.Files = append(plan.Files, planned)
	}
	if !req.Commit {
		ignore, err := s.planIgnore(req.Tool)
		if err != nil {
			return Plan{}, err
		}
		plan.Ignore = ignore
	}
	return plan, nil
}

// planFile decides what would happen to one wholly owned file.
func (s *Service) planFile(f PlannedFile, adopt bool) (PlannedFile, error) {
	existing, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(f.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		f.Action = ActionCreate
		return f, nil
	}
	if err != nil {
		return PlannedFile{}, fmt.Errorf("host: read %s: %w", f.Path, err)
	}
	switch {
	case string(existing) == string(f.content):
		f.Action = ActionUnchanged
	case Owned(f.Path, existing):
		f.Action = ActionUpdate
	case adopt:
		f.Action = ActionUpdate
		f.Reason = "adopted: the existing file was replaced on request"
	default:
		f.Action = ActionConflict
		f.Reason = describeConflict(f.Path)
	}
	return f, nil
}

// planSettings projects Homonto's hooks into Claude's shared settings
// document.
//
// Only the hook entries that invoke Homonto are rewritten. Everything else
// in the file — the user's own hooks, their permissions, their model
// choice — is read, kept, and written back unchanged. A shared document is
// not ours to normalize.
func (s *Service) planSettings(path string, req InstallRequest) (PlannedFile, error) {
	planned := PlannedFile{Path: path, Mode: 0o644}
	document := map[string]any{}
	existing, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(path)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return PlannedFile{}, fmt.Errorf("host: read %s: %w", path, err)
	default:
		if err := json.Unmarshal(existing, &document); err != nil {
			// A settings file Homonto cannot parse is a settings file it
			// must not rewrite: emitting a plan that replaces a document
			// it failed to read would destroy whatever was in there.
			planned.Action = ActionConflict
			planned.Reason = fmt.Sprintf("%s is not valid JSON (%v), so its hooks cannot be projected", path, err)
			return planned, nil
		}
	}

	merged, err := mergeHooks(document, req.binary())
	if err != nil {
		return PlannedFile{}, err
	}
	encoded, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return PlannedFile{}, fmt.Errorf("host: encode %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	planned.content = encoded
	switch {
	case errors.Is(err, fs.ErrNotExist) || len(existing) == 0:
		planned.Action = ActionCreate
	case string(existing) == string(encoded):
		planned.Action = ActionUnchanged
	default:
		planned.Action = ActionUpdate
	}
	return planned, nil
}

// mergeHooks replaces Homonto's hook entries in a settings document and
// leaves every other entry alone.
func mergeHooks(document map[string]any, binary string) (map[string]any, error) {
	desired, err := hookEntries(binary)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(document)+1)
	for k, v := range document {
		out[k] = v
	}
	hooks, _ := out["hooks"].(map[string]any)
	merged := make(map[string]any, len(hooks)+len(desired))
	for event, entries := range hooks {
		kept := stripHomontoEntries(entries, binary)
		if kept != nil {
			merged[event] = kept
		}
	}
	for event, entries := range desired {
		ours, ok := entries.([]any)
		if !ok {
			return nil, fmt.Errorf("host: embedded hooks for %q are not a list", event)
		}
		existing, _ := merged[event].([]any)
		merged[event] = append(append([]any{}, existing...), ours...)
	}
	out["hooks"] = merged
	return out, nil
}

// stripHomontoEntries removes Homonto's own hook entries from one event's
// list, returning what the user had.
func stripHomontoEntries(entries any, binary string) []any {
	list, ok := entries.([]any)
	if !ok {
		return nil
	}
	var kept []any
	for _, entry := range list {
		group, ok := entry.(map[string]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}
		hooks, ok := group["hooks"].([]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}
		var keptHooks []any
		for _, h := range hooks {
			hook, ok := h.(map[string]any)
			if !ok {
				keptHooks = append(keptHooks, h)
				continue
			}
			command, _ := hook["command"].(string)
			if isHomontoHook(command, binary) {
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 {
			continue
		}
		group["hooks"] = keptHooks
		kept = append(kept, group)
	}
	return kept
}

// planIgnore returns the .gitignore entries that are missing.
func (s *Service) planIgnore(tool Tool) ([]string, error) {
	want, err := ignoreEntries(tool)
	if err != nil {
		return nil, err
	}
	existing, err := os.ReadFile(filepath.Join(s.root, ".gitignore"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("host: read .gitignore: %w", err)
	}
	present := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, entry := range want {
		if present[entry] || present[strings.TrimSuffix(entry, "/")] {
			continue
		}
		missing = append(missing, entry)
	}
	return missing, nil
}

// ApplyInstall writes a plan.
//
// A plan carrying a conflict is refused WHOLE. Installing the files that
// happen not to conflict would leave a half-installed integration whose
// wrappers and hooks disagree about which version of Homonto they speak
// to, which is worse than not installing.
func (s *Service) ApplyInstall(ctx context.Context, plan Plan) error {
	if conflicts := plan.Conflicts(); len(conflicts) > 0 {
		paths := make([]string, 0, len(conflicts))
		for _, c := range conflicts {
			paths = append(paths, c.Path)
		}
		return fmt.Errorf("host: %v: %w", paths, ErrConflict)
	}
	for _, f := range plan.Files {
		if !f.Action.Writes() {
			continue
		}
		if err := s.writeFile(f); err != nil {
			return err
		}
	}
	return s.appendIgnore(plan.Ignore)
}

// writeFile writes one planned file atomically, creating its parents.
func (s *Service) writeFile(f PlannedFile) error {
	abs := filepath.Join(s.root, filepath.FromSlash(f.Path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("host: create %s: %w", filepath.Dir(f.Path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".homonto-host-*")
	if err != nil {
		return fmt.Errorf("host: stage %s: %w", f.Path, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(f.content); err != nil {
		tmp.Close()
		return fmt.Errorf("host: write %s: %w", f.Path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("host: sync %s: %w", f.Path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("host: close %s: %w", f.Path, err)
	}
	if err := os.Chmod(name, f.Mode); err != nil {
		return fmt.Errorf("host: chmod %s: %w", f.Path, err)
	}
	if err := os.Rename(name, abs); err != nil {
		return fmt.Errorf("host: install %s: %w", f.Path, err)
	}
	return nil
}

// appendIgnore adds the missing .gitignore entries.
func (s *Service) appendIgnore(entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	path := filepath.Join(s.root, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("host: read .gitignore: %w", err)
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n# Homonto host integration (project-local; remove these lines to commit it)\n")
	for _, entry := range entries {
		b.WriteString(entry)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("host: write .gitignore: %w", err)
	}
	return nil
}

// Observe reports what is actually installed for one tool.
func (s *Service) Observe(ctx context.Context, target Target, req InstallRequest) (Observation, error) {
	req.Tool = target.Tool
	if err := req.Validate(); err != nil {
		return Observation{}, err
	}
	obs := Observation{Target: target}
	files, err := generatedFiles(req)
	if err != nil {
		return Observation{}, err
	}
	for _, f := range files {
		existing, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(f.Path)))
		if errors.Is(err, fs.ErrNotExist) {
			obs.Missing = append(obs.Missing, f.Path)
			continue
		}
		if err != nil {
			return Observation{}, fmt.Errorf("host: read %s: %w", f.Path, err)
		}
		switch {
		case string(existing) == string(f.content):
			obs.Installed = append(obs.Installed, f.Path)
		case Owned(f.Path, existing):
			// The marker matches its own body, so Homonto wrote it — an
			// older version of it. That is stale, not modified.
			obs.Installed = append(obs.Installed, f.Path)
		case hasMarker(existing):
			obs.Modified = append(obs.Modified, f.Path)
		default:
			obs.Foreign = append(obs.Foreign, f.Path)
		}
	}
	sort.Strings(obs.Installed)
	sort.Strings(obs.Modified)
	sort.Strings(obs.Missing)
	sort.Strings(obs.Foreign)
	return obs, nil
}

// hasMarker reports whether content carries an ownership marker at all,
// whether or not it still matches. It is what separates "Homonto wrote
// this and someone edited it" from "something else wrote this".
func hasMarker(content []byte) bool {
	_, _, ok := splitMarker(content)
	return ok
}
