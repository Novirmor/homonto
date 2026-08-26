package app

import (
	"context"

	"github.com/noviopenworks/homonto/internal/host"
)

// PlanHostInstall works out what installing would do, without doing it.
func (a *App) PlanHostInstall(ctx context.Context, opts host.InstallOptions) ([]host.Plan, error) {
	return host.PlanInstalls(ctx, a.controlRoot(), a.cfg.Workspace.Workflow, opts)
}

// ApplyHostInstall writes the plans.
func (a *App) ApplyHostInstall(ctx context.Context, plans []host.Plan) error {
	return host.ApplyInstalls(ctx, a.controlRoot(), plans)
}

// ObserveHosts reports what is installed for each detected tool.
func (a *App) ObserveHosts(ctx context.Context, binary string) ([]host.Observation, error) {
	return host.ObserveInstalls(ctx, a.controlRoot(), a.cfg.Workspace.Workflow, binary)
}
