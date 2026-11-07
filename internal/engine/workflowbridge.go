package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/fsutil"
)

const workflowBridgePlugin = "homonto-workflow"

func (e *Engine) workflowBindingPath() string {
	return filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "binding.json")
}

func (e *Engine) workflowBinding() ([]byte, error) {
	if !filepath.IsAbs(e.ConfigPath) || filepath.Clean(e.ConfigPath) != e.ConfigPath || filepath.Dir(e.ConfigPath) != e.ProjectRoot {
		return nil, fmt.Errorf("workflow bridge: invalid selected config identity %q", e.ConfigPath)
	}
	coordinator := "homonto"
	for name, agent := range e.Cfg.Subagents {
		if catalogName, ok := config.SubagentCatalogName(agent.Source); ok && catalogName == "homonto" {
			coordinator = name
		}
	}
	hosts := e.Cfg.HostingProviders()
	data, err := json.MarshalIndent(struct {
		Version          int    `json:"version"`
		ConfigPath       string `json:"configPath"`
		ConfigRoot       string `json:"configRoot"`
		Coordinator      string `json:"coordinator"`
		GithubEnabled    bool   `json:"githubEnabled"`
		GitlabEnabled    bool   `json:"gitlabEnabled"`
		BitbucketEnabled bool   `json:"bitbucketEnabled"`
	}{1, e.ConfigPath, e.ProjectRoot, coordinator, hosts["github"], hosts["gitlab"], hosts["bitbucket"]}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (e *Engine) workflowBindingPresent(want []byte) bool {
	if len(want) == 0 {
		return true
	}
	path := e.workflowBindingPath()
	if err := fsutil.RequireRealParents(e.StateDir, filepath.Dir(path)); err != nil {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && bytes.Equal(data, want)
}

// workflowBridgeSource is the removed V1 bridge entry (ADR 0079). Its link stays
// in workflowPluginLinks, permanently disabled, so apply still removes a stale
// owned symlink; removal compares only the link target, so the file need not exist.
func (e *Engine) workflowBridgeSource() string {
	return filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "plugin.ts")
}

func (e *Engine) workflowBridgeDestination() string {
	return filepath.Join(e.ProjectRoot, ".opencode", "plugins", workflowBridgePlugin+".ts")
}

func (e *Engine) workflowContextDestination() string {
	return filepath.Join(e.ProjectRoot, ".opencode", "plugins", workflowBridgePlugin+"-context")
}

type workflowPluginLink struct {
	source      string
	destination string
	enabled     bool
	directory   bool
}

func (e *Engine) workflowPluginLinks() []workflowPluginLink {
	return []workflowPluginLink{
		{source: e.workflowBridgeSource(), destination: e.workflowBridgeDestination(), enabled: false},
		{source: filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "v2.ts"), destination: e.workflowContextDestination() + ".ts"},
		{source: filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin), destination: e.workflowContextDestination(), enabled: e.Cfg.WorkflowContextEnabled(), directory: true},
	}
}

// Both endpoints move with the project (ADR 0026). Keep the exact relative
// spelling as the owned target; matching a foreign path's suffix is not proof.
func (link workflowPluginLink) target() (string, error) {
	return filepath.Rel(filepath.Dir(link.destination), link.source)
}

// workflowBridgePresent reports whether the configured bridge state has
// converged. A foreign file deliberately remains "not present" so apply can
// name the conflict instead of treating an untrusted replacement as managed.
func (e *Engine) workflowBridgePresent() bool {
	for _, link := range e.workflowPluginLinks() {
		if !e.workflowPluginLinkPresent(link) {
			return false
		}
	}
	return true
}

func (e *Engine) workflowPluginLinkPresent(link workflowPluginLink) bool {
	dst := link.destination
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(dst)); err != nil {
		return false
	}
	if !link.enabled {
		_, err := os.Lstat(dst)
		return os.IsNotExist(err)
	}
	want, err := link.target()
	if err != nil {
		return false
	}
	target, err := os.Readlink(dst)
	return err == nil && target == want
}

// ensureWorkflowBridge creates or removes only the exact symlink homonto owns.
// The source remains in the generated catalog even when disabled, so toggling
// the setting is reversible without a fetch or a copied plugin file.
func (e *Engine) ensureWorkflowBridge() error {
	links := e.workflowPluginLinks()
	for _, link := range links {
		if err := e.ensureWorkflowPluginLink(link, true); err != nil {
			return err
		}
	}
	for _, enabled := range []bool{false, true} {
		for _, link := range links {
			if link.enabled == enabled {
				if err := e.ensureWorkflowPluginLink(link, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (e *Engine) preflightWorkflowPluginDestination(link workflowPluginLink) error {
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(link.destination)); err != nil {
		return fmt.Errorf("workflow bridge: unsafe plugin directory: %w", err)
	}
	want, err := link.target()
	if err != nil {
		return fmt.Errorf("workflow bridge: relative plugin target: %w", err)
	}
	target, err := os.Readlink(link.destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workflow bridge: %s exists and is not a homonto-managed symlink; not overwriting", link.destination)
	}
	if target != want && target != link.source {
		return fmt.Errorf("workflow bridge: %s points outside homonto's catalog; not replacing", link.destination)
	}
	return nil
}

func (e *Engine) ensureWorkflowPluginLink(link workflowPluginLink, dryRun bool) error {
	dst := link.destination
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(dst)); err != nil {
		return fmt.Errorf("workflow bridge: unsafe plugin directory: %w", err)
	}
	want, err := link.target()
	if err != nil {
		return fmt.Errorf("workflow bridge: relative plugin target: %w", err)
	}
	if !link.enabled {
		target, err := os.Readlink(dst)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("workflow bridge: %s exists and is not a homonto-managed symlink; not removing", dst)
		}
		if target != want && target != link.source {
			return fmt.Errorf("workflow bridge: %s points outside homonto's catalog; not removing", dst)
		}
		if dryRun {
			return nil
		}
		return os.Remove(dst)
	}
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(link.source)); err != nil {
		return fmt.Errorf("workflow bridge: unsafe catalog source: %w", err)
	}
	info, err := os.Lstat(link.source)
	if err != nil {
		return fmt.Errorf("workflow bridge: catalog source missing: %w", err)
	}
	if (link.directory && !info.IsDir()) || (!link.directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("workflow bridge: catalog source has wrong type: %s", link.source)
	}
	if link.directory && !allPluginDirsExist(e.PluginCatalogRoot, []string{workflowBridgePlugin}, true) {
		return fmt.Errorf("workflow bridge: catalog source incomplete: %s", link.source)
	}
	if target, err := os.Readlink(dst); err == nil {
		if target == want {
			return nil
		}
		if target != link.source {
			return fmt.Errorf("workflow bridge: %s points outside homonto's catalog; not replacing", dst)
		}
		if dryRun {
			return nil
		}
		// Migrate only the exact absolute target of this current project. A stale
		// pre-move absolute target has no recorded bridge provenance to authenticate.
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("workflow bridge: migrate absolute link: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("workflow bridge: %s exists and is not a homonto-managed symlink; not overwriting", dst)
	}
	if dryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("workflow bridge: create plugin directory: %w", err)
	}
	if err := os.Symlink(want, dst); err != nil {
		return fmt.Errorf("workflow bridge: link plugin: %w", err)
	}
	return nil
}
