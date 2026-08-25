package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/host"
)

// HostInstallOptions configure installing the host integrations.
type HostInstallOptions struct {
	// Tools names which host tools to install for. Empty means the ones
	// detected in this control repository — installing into a tool nobody
	// uses here would leave files the user never asked for.
	Tools []string
	// Adopt replaces generated files that were edited by hand.
	Adopt bool
	// Commit opts into committing the generated files.
	Commit bool
	// Binary overrides how the wrappers invoke Homonto.
	Binary string
}

// PlanHostInstall works out what installing would do, without doing it.
func (a *App) PlanHostInstall(ctx context.Context, opts HostInstallOptions) ([]host.Plan, error) {
	controlRoot := filepath.Join(a.root, filepath.FromSlash(normalizePath(a.cfg.Control.Path)))
	service, err := host.NewService(controlRoot)
	if err != nil {
		return nil, err
	}
	tools, err := a.hostTools(controlRoot, opts.Tools)
	if err != nil {
		return nil, err
	}
	plans := make([]host.Plan, 0, len(tools))
	for _, tool := range tools {
		plan, err := service.PlanInstall(ctx, host.InstallRequest{
			Tool:     tool,
			Workflow: a.cfg.Workspace.Workflow,
			Binary:   opts.Binary,
			Adopt:    opts.Adopt,
			Commit:   opts.Commit,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// ApplyHostInstall writes the plans.
func (a *App) ApplyHostInstall(ctx context.Context, plans []host.Plan) error {
	controlRoot := filepath.Join(a.root, filepath.FromSlash(normalizePath(a.cfg.Control.Path)))
	service, err := host.NewService(controlRoot)
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

// ObserveHosts reports what is installed for each detected tool.
func (a *App) ObserveHosts(ctx context.Context, opts HostInstallOptions) ([]host.Observation, error) {
	controlRoot := filepath.Join(a.root, filepath.FromSlash(normalizePath(a.cfg.Control.Path)))
	service, err := host.NewService(controlRoot)
	if err != nil {
		return nil, err
	}
	targets, err := host.Detect(controlRoot)
	if err != nil {
		return nil, err
	}
	var out []host.Observation
	for _, target := range targets {
		if !target.Present {
			continue
		}
		obs, err := service.Observe(ctx, target, host.InstallRequest{
			Tool: target.Tool, Workflow: a.cfg.Workspace.Workflow, Binary: opts.Binary,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, obs)
	}
	return out, nil
}

// hostTools resolves which tools to install for.
func (a *App) hostTools(controlRoot string, requested []string) ([]host.Tool, error) {
	if len(requested) > 0 {
		out := make([]host.Tool, 0, len(requested))
		for _, name := range requested {
			tool := host.Tool(name)
			if !tool.Known() {
				return nil, fmt.Errorf("app: %q is not a supported host tool", name)
			}
			out = append(out, tool)
		}
		return out, nil
	}
	targets, err := host.Detect(controlRoot)
	if err != nil {
		return nil, err
	}
	var out []host.Tool
	for _, target := range targets {
		if target.Present {
			out = append(out, target.Tool)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"app: no host tool is in use here; name one with --tool (%s or %s)",
			host.ToolClaude, host.ToolOpenCode)
	}
	return out, nil
}
