package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/Novirmor/homonto/internal/routing"
)

// ResolvedRoute is one agent's model source with its resolved selection
// (nil for a pin or for an agent nothing routes).
type ResolvedRoute struct {
	config.AgentRoute
	Selection *routing.Selection `json:"selection,omitempty"`
}

// RouteReport is the resolved routing for the opencode harness.
type RouteReport struct {
	Routes []ResolvedRoute `json:"routes"`
	// Discovery is not-run, ok or failed (ADR 0100).
	Discovery      string `json:"discovery"`
	DiscoveryError string `json:"discoveryError,omitempty"`
	Models         int    `json:"models,omitempty"`
}

// RoutingRecordFile is where apply records the routes it stamped into agent
// files (ADR 0102), under the state directory.
const RoutingRecordFile = "routing.json"

// RoutingRecord is the applied routing, as written at apply.
type RoutingRecord struct {
	AppliedAt string `json:"appliedAt"`
	RouteReport
}

// newRouteDiscoverer builds the harness model discovery for a project.
// Build already renders agents, so tests replace this before Build rather
// than setting RouteDiscoverer afterwards.
var newRouteDiscoverer = func(projectRoot string) routing.Discoverer {
	return routing.OpenCodeCLI{Dir: projectRoot}
}

// Routes resolves every agent's model for the opencode harness. Discovery
// runs at most once per engine (one command invocation), and only if an
// auto policy applies, so plan and the apply of that plan agree.
func (e *Engine) Routes() (RouteReport, error) {
	if e.routeReport != nil {
		return *e.routeReport, nil
	}
	if e.routeErr != nil {
		return RouteReport{}, e.routeErr
	}
	report, err := e.resolveRoutes(context.Background())
	if err != nil {
		e.routeErr = err
		return RouteReport{}, err
	}
	e.routeReport = &report
	return report, nil
}

// RoutesFor resolves every agent's model for one harness. OpenCode is
// Routes; Claude Code has no model listing command, so its auto policies
// choose among the models [routing.models.claude] describes (ADR 0100).
func (e *Engine) RoutesFor(tool string) (RouteReport, error) {
	if tool == "opencode" {
		return e.Routes()
	}
	if r, ok := e.otherRoutes[tool]; ok {
		return r, nil
	}
	discover := routing.Static(slices.Sorted(maps.Keys(e.Cfg.Routing.ModelsFor(tool))))
	report, err := e.resolveRoutesFor(context.Background(), tool, discover)
	if err != nil {
		return RouteReport{}, err
	}
	if e.otherRoutes == nil {
		e.otherRoutes = map[string]RouteReport{}
	}
	e.otherRoutes[tool] = report
	return report, nil
}

func (e *Engine) resolveRoutes(ctx context.Context) (RouteReport, error) {
	discover := e.RouteDiscoverer
	if discover == nil {
		discover = newRouteDiscoverer(e.ProjectRoot)
	}
	return e.resolveRoutesFor(ctx, "opencode", discover)
}

func (e *Engine) resolveRoutesFor(ctx context.Context, tool string, discover routing.Discoverer) (RouteReport, error) {
	agentRoutes, err := e.Cfg.AgentRoutes(tool)
	if err != nil {
		return RouteReport{}, err
	}
	resolver := &routing.Resolver{Discover: discover, Models: e.Cfg.Routing.ModelsFor(tool)}
	var report RouteReport
	for _, name := range slices.Sorted(maps.Keys(agentRoutes)) {
		r := ResolvedRoute{AgentRoute: agentRoutes[name]}
		if r.From == config.RouteFromPolicy {
			sel, err := resolver.Resolve(ctx, r.Selector, *r.Policy)
			if err != nil {
				return RouteReport{}, err
			}
			r.Selection = &sel
		}
		report.Routes = append(report.Routes, r)
	}
	state, models, derr := resolver.DiscoveryState()
	report.Discovery, report.Models = state, models
	if derr != nil {
		report.DiscoveryError = derr.Error()
	}
	return report, nil
}

// policyModel returns the model a policy chose for an external agent.
func (r RouteReport) policyModel(agent string) (*routing.Selection, bool) {
	for _, route := range r.Routes {
		if route.Agent == agent && !route.CatalogKeyed && route.Selection != nil {
			return route.Selection, true
		}
	}
	return nil, false
}

// routeWarnings are the routing facts plan surfaces: a policy that did not
// reach an agent, and an auto policy that fell back.
func (r RouteReport) routeWarnings() []string {
	var out []string
	for _, route := range r.Routes {
		if route.Note != "" {
			out = append(out, fmt.Sprintf("routing: %s: %s; it uses %s", route.Agent, route.Note, routeSourceLabel(route)))
		}
		if route.Selection != nil && route.Selection.Fallback {
			out = append(out, fmt.Sprintf("routing: %s uses fallback %s from %s: %s", route.Agent, route.Selection.Model, route.Selector, route.Selection.Reason))
		}
	}
	return out
}

func routeSourceLabel(r ResolvedRoute) string {
	if r.Selector == "" {
		return "no policy"
	}
	return r.Selector
}

// recordRoutes writes the applied routing record, or removes a stale one
// when no policy applies any more.
func (e *Engine) recordRoutes() error {
	path := filepath.Join(e.StateDir, RoutingRecordFile)
	report, err := e.Routes()
	if err != nil {
		return err
	}
	policyRouted := slices.ContainsFunc(report.Routes, func(r ResolvedRoute) bool { return r.From == config.RouteFromPolicy })
	if !policyRouted {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(RoutingRecord{AppliedAt: time.Now().UTC().Format(time.RFC3339), RouteReport: report}, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, append(data, '\n'))
}

// AppliedRoutes reads the routing record the last apply wrote. ok is false
// when no policy routed anything at that apply.
func (e *Engine) AppliedRoutes() (RoutingRecord, bool, error) {
	data, err := os.ReadFile(filepath.Join(e.StateDir, RoutingRecordFile))
	if errors.Is(err, fs.ErrNotExist) {
		return RoutingRecord{}, false, nil
	}
	if err != nil {
		return RoutingRecord{}, false, err
	}
	var rec RoutingRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return RoutingRecord{}, false, fmt.Errorf("%s: %w", RoutingRecordFile, err)
	}
	return rec, true, nil
}
