package config

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/Novirmor/homonto/internal/catalog"
)

// Routing is the [routing] table: model-routing policies addressed by
// harness-independent selectors, with harness-native policy fields
// (ADR 0098, ADR 0099): one field per harness, in that harness's own model
// spelling.
type Routing struct {
	Default   RouteSelector            `toml:"default"`
	Workflows map[string]RouteSelector `toml:"workflows"`
	Engines   map[string]EngineRouting `toml:"engines"`
	// Models describes execution models for automatic selection, per
	// harness, keyed by the harness's own model id. Nothing is inferred:
	// a constraint on cost, latency or a need only matches models the
	// config describes here (ADR 0100).
	Models struct {
		OpenCode map[string]ModelInfo `toml:"opencode"`
		Claude   map[string]ModelInfo `toml:"claude"`
	} `toml:"models"`
}

// ModelsFor returns the model descriptions for one harness.
func (r Routing) ModelsFor(tool string) map[string]ModelInfo {
	if tool == "claude" {
		return r.Models.Claude
	}
	return r.Models.OpenCode
}

// RouteSelector holds one selector's policy per harness.
type RouteSelector struct {
	OpenCode *RoutePolicy `toml:"opencode"`
	Claude   *RoutePolicy `toml:"claude"`
}

// For returns the selector's policy for one harness.
func (s RouteSelector) For(tool string) *RoutePolicy {
	if tool == "claude" {
		return s.Claude
	}
	return s.OpenCode
}

// EngineRouting is an engine selector: its own policy plus one per stage.
type EngineRouting struct {
	OpenCode *RoutePolicy             `toml:"opencode"`
	Claude   *RoutePolicy             `toml:"claude"`
	Stages   map[string]RouteSelector `toml:"stages"`
}

// For returns the engine's own policy for one harness.
func (e EngineRouting) For(tool string) *RoutePolicy {
	if tool == "claude" {
		return e.Claude
	}
	return e.OpenCode
}

// Routing modes.
const (
	RouteStatic = "static"
	RouteAuto   = "auto"
)

// Auto-selection orderings.
const (
	PreferListed  = "listed"
	PreferCost    = "cost"
	PreferLatency = "latency"
)

// RoutePolicy is one selector's policy for one harness. A static policy
// names a model; an auto policy selects one at plan time from the models the
// harness reports, under its constraints, and falls back deterministically.
type RoutePolicy struct {
	Mode    string `toml:"mode"` // static (default) | auto
	Model   string `toml:"model"`
	Variant string `toml:"variant"`
	// Auto fields.
	Candidates []string `toml:"candidates"` // optional ordered shortlist
	Providers  []string `toml:"providers"`  // allowed provider prefixes
	Needs      []string `toml:"needs"`      // tags every selected model must carry
	MaxCost    *float64 `toml:"max_cost"`
	MaxLatency *float64 `toml:"max_latency"`
	Prefer     string   `toml:"prefer"` // listed (default) | cost | latency
	Fallback   string   `toml:"fallback"`
}

// ModeOrDefault returns the policy's mode, static when unset.
func (p RoutePolicy) ModeOrDefault() string {
	if p.Mode == "" {
		return RouteStatic
	}
	return p.Mode
}

// PreferOrDefault returns the auto ordering, listed when unset.
func (p RoutePolicy) PreferOrDefault() string {
	if p.Prefer == "" {
		return PreferListed
	}
	return p.Prefer
}

// ModelInfo is what the config states about one model. Units are the
// operator's own (for example USD per million output tokens, or seconds to
// first token); homonto only compares them.
type ModelInfo struct {
	Cost    *float64 `toml:"cost"`
	Latency *float64 `toml:"latency"`
	Tags    []string `toml:"tags"`
}

var routeTag = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Empty reports whether no policy or model description is configured.
func (r Routing) Empty() bool {
	return r.Default.OpenCode == nil && r.Default.Claude == nil && len(r.Workflows) == 0 && len(r.Engines) == 0 &&
		len(r.Models.OpenCode) == 0 && len(r.Models.Claude) == 0
}

// validateRouting checks the policies' own shape and that every selector
// names a declared package. Stage names need the package manifests and are
// checked by validateRoutingStages once the catalog resolves.
func validateRouting(c *Config) error {
	r := c.Routing
	for _, name := range slices.Sorted(maps.Keys(r.Workflows)) {
		if _, ok := c.Workflows[name]; !ok {
			return fmt.Errorf("parse config: routing.workflows.%s names no declared workflow; declare [workflows.%s] or remove the policy", name, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(r.Engines)) {
		if _, ok := c.Engines[name]; !ok {
			return fmt.Errorf("parse config: routing.engines.%s names no declared engine; declare [engines.%s] or remove the policy", name, name)
		}
	}
	for _, tool := range HarnessIDs() {
		if err := validateRoutePolicy(tool, "routing.default."+tool, r.Default.For(tool)); err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(r.Workflows)) {
			if err := validateRoutePolicy(tool, "routing.workflows."+name+"."+tool, r.Workflows[name].For(tool)); err != nil {
				return err
			}
		}
		for _, name := range slices.Sorted(maps.Keys(r.Engines)) {
			er := r.Engines[name]
			if err := validateRoutePolicy(tool, "routing.engines."+name+"."+tool, er.For(tool)); err != nil {
				return err
			}
			for _, stage := range slices.Sorted(maps.Keys(er.Stages)) {
				if err := validateRoutePolicy(tool, "routing.engines."+name+".stages."+stage+"."+tool, er.Stages[stage].For(tool)); err != nil {
					return err
				}
			}
		}
		models := r.ModelsFor(tool)
		for _, id := range slices.Sorted(maps.Keys(models)) {
			label := fmt.Sprintf("routing.models.%s.%q", tool, id)
			if err := routeModelID(tool, label, id); err != nil {
				return err
			}
			m := models[id]
			for _, v := range []*float64{m.Cost, m.Latency} {
				if v != nil && !(*v >= 0) {
					return fmt.Errorf("parse config: %s cost and latency must be non-negative numbers", label)
				}
			}
			if err := routeTags(label+" tags", m.Tags); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRoutePolicy(tool, label string, p *RoutePolicy) error {
	if p == nil {
		return nil
	}
	auto := len(p.Candidates) > 0 || len(p.Providers) > 0 || len(p.Needs) > 0 || p.MaxCost != nil || p.MaxLatency != nil || p.Prefer != "" || p.Fallback != ""
	switch p.ModeOrDefault() {
	case RouteStatic:
		if p.Model == "" {
			return fmt.Errorf("parse config: %s is a static policy and needs a model (or set mode = \"auto\")", label)
		}
		if auto {
			return fmt.Errorf("parse config: %s is a static policy; candidates, providers, needs, max_cost, max_latency, prefer and fallback belong to mode = \"auto\"", label)
		}
		if err := routeModelID(tool, label+" model", p.Model); err != nil {
			return err
		}
		return validateModelSpec(tool, label, ModelRoute{Model: p.Model, Variant: p.Variant}, true)
	case RouteAuto:
		if p.Model != "" || p.Variant != "" {
			return fmt.Errorf("parse config: %s is an auto policy; it selects the model itself, so model and variant are not allowed (name a fallback instead)", label)
		}
		if p.Fallback == "" {
			return fmt.Errorf("parse config: %s is an auto policy and needs a fallback model for when discovery fails or nothing qualifies", label)
		}
		if err := routeModelID(tool, label+" fallback", p.Fallback); err != nil {
			return err
		}
		for _, id := range p.Candidates {
			if err := routeModelID(tool, label+" candidates", id); err != nil {
				return err
			}
		}
		if tool == "claude" && len(p.Providers) > 0 {
			return fmt.Errorf("parse config: %s providers do not apply to Claude Code, whose model ids carry no provider", label)
		}
		if len(slices.Compact(slices.Sorted(slices.Values(p.Candidates)))) != len(p.Candidates) {
			return fmt.Errorf("parse config: %s candidates must be distinct", label)
		}
		for _, provider := range p.Providers {
			if provider == "" || strings.ContainsAny(provider, "/ \t") {
				return fmt.Errorf("parse config: %s providers must be provider ids such as \"anthropic\" (no '/' or spaces)", label)
			}
		}
		if err := routeTags(label+" needs", p.Needs); err != nil {
			return err
		}
		for _, v := range []*float64{p.MaxCost, p.MaxLatency} {
			if v != nil && !(*v >= 0) {
				return fmt.Errorf("parse config: %s max_cost and max_latency must be non-negative numbers", label)
			}
		}
		switch p.PreferOrDefault() {
		case PreferListed, PreferCost, PreferLatency:
		default:
			return fmt.Errorf("parse config: %s prefer %q is not one of listed, cost, latency", label, p.Prefer)
		}
		return nil
	default:
		return fmt.Errorf("parse config: %s mode %q is not static or auto", label, p.Mode)
	}
}

// routeModelID requires the harness's model spelling: OpenCode's
// provider/model form, so a provider constraint can always be judged, or a
// Claude Code alias or model id.
func routeModelID(tool, label, id string) error {
	if tool == "claude" {
		if id == "" || strings.ContainsAny(id, "/ \t\n#") {
			return fmt.Errorf("parse config: %s %q must be a Claude Code alias (sonnet, opus, haiku) or a model id such as claude-sonnet-5-5", label, id)
		}
		return nil
	}
	provider, model, ok := strings.Cut(id, "/")
	if !ok || provider == "" || model == "" || strings.ContainsAny(id, " \t\n#") {
		return fmt.Errorf("parse config: %s %q must be a provider/model id such as \"anthropic/claude-sonnet-5\"", label, id)
	}
	return nil
}

func routeTags(label string, tags []string) error {
	for _, tag := range tags {
		if !routeTag.MatchString(tag) {
			return fmt.Errorf("parse config: %s %q must be a lowercase tag (letters, digits, '-', '_')", label, tag)
		}
	}
	return nil
}

// validateRoutingStages checks stage selectors against the engines'
// declared stages. A remote engine not yet injected is skipped, like
// package settings, and judged again once its verified root is.
func (c *Config) validateRoutingStages(cl *catalog.Catalog) error {
	for _, name := range slices.Sorted(maps.Keys(c.Routing.Engines)) {
		stages := c.Routing.Engines[name].Stages
		if len(stages) == 0 {
			continue
		}
		fw, ok := c.packageFramework(cl, name)
		if !ok {
			continue
		}
		if fw.Engine == nil {
			return fmt.Errorf("parse config: routing.engines.%s has stage policies, but %s declares no [engine] stages", name, name)
		}
		for _, stage := range slices.Sorted(maps.Keys(stages)) {
			if !slices.Contains(fw.Engine.Stages, stage) {
				return fmt.Errorf("parse config: routing.engines.%s.stages.%s is not a stage of %s (%s)", name, stage, name, strings.Join(fw.Engine.Stages, ", "))
			}
		}
	}
	return nil
}

// Route sources: where an agent's model comes from.
const (
	RouteFromPin    = "pin"
	RouteFromPolicy = "policy"
	RouteFromNone   = "none"
)

// AgentRoute is how one rendered agent's model is chosen for one harness.
type AgentRoute struct {
	Agent string `json:"agent"` // installed identity (catalog name, or entry name for external agents)
	Tool  string `json:"tool"`
	// From is pin (an explicit [subagents.<name>.<tool>] model), policy or
	// none (no model source: config validation reports it).
	From     string `json:"from"`
	Selector string `json:"selector,omitempty"` // the config key that chose the model or policy
	// Policy is the applied policy when From is policy. An auto policy is
	// resolved to a concrete model at plan time (internal/routing).
	Policy *RoutePolicy `json:"policy,omitempty"`
	Pin    ModelRoute   `json:"-"`
	// CatalogKeyed is true for an agent materialized from the catalog (keyed
	// by catalog name), false for an external agent rendered by intent.
	CatalogKeyed bool `json:"-"`
	// Note explains a policy that did not apply because the agent's stages or
	// owners disagree. It is shown by plan and explain.
	Note string `json:"note,omitempty"`
}

// scope is one place an agent's work runs: a package (engine or workflow)
// and, for an engine agent, one bound stage.
type routeScope struct {
	kind, pkg, stage string
}

type routeCandidate struct {
	selector string
	policy   *RoutePolicy
}

// AgentRoutes resolves every rendered agent's model source for tool. A
// model pin wins; otherwise the most specific policy wins (stage > engine >
// workflow > default, ADR 0098), as long as every place the agent serves
// agrees on it. When an agent's bound stages, or its owning packages,
// resolve to different policies, the next coarser level that agrees applies
// and Note says why (ADR 0099). The shared coordinator serves every package,
// so only [routing.default] reaches it.
func (c *Config) AgentRoutes(tool string) (map[string]AgentRoute, error) {
	entries, err := c.ExpandedSubagentEntriesForTool(tool)
	if err != nil {
		return nil, err
	}
	var cl *catalog.Catalog
	out := map[string]AgentRoute{}
	for _, entry := range entries {
		identity, catalogKeyed := SubagentCatalogName(entry.Resource.Source)
		if !catalogKeyed {
			identity = entry.Name
		}
		route := AgentRoute{Agent: identity, Tool: tool, From: RouteFromNone, CatalogKeyed: catalogKeyed}
		if pin, label := c.modelPin(identity, entry.Name, catalogKeyed, tool); pin.Model != "" {
			route.From, route.Selector, route.Pin = RouteFromPin, label, pin
			out[identity] = route
			continue
		} else {
			route.Pin = pin
		}
		var scopes []routeScope
		if identity != catalog.SharedCoordinatorAgent {
			for _, origin := range entry.Origins {
				if origin.Kind != "framework" {
					scopes = append(scopes, routeScope{})
					continue
				}
				if cl == nil {
					if cl, err = c.FrameworkCatalog(); err != nil {
						return nil, err
					}
				}
				fw, ok := c.packageFramework(cl, origin.Framework)
				switch {
				case !ok:
					scopes = append(scopes, routeScope{})
				case fw.Kind == "engine":
					stages := []string(nil)
					if fw.Engine != nil {
						stages = fw.Engine.StagesOf(entry.Name)
					}
					if len(stages) == 0 {
						scopes = append(scopes, routeScope{kind: "engine", pkg: origin.Framework})
					}
					for _, stage := range stages {
						scopes = append(scopes, routeScope{kind: "engine", pkg: origin.Framework, stage: stage})
					}
				case fw.Kind == "workflow":
					scopes = append(scopes, routeScope{kind: "workflow", pkg: origin.Framework})
				default:
					scopes = append(scopes, routeScope{})
				}
			}
		}
		if len(scopes) == 0 {
			scopes = []routeScope{{}}
		}
		route.Selector, route.Policy, route.Note = c.agreeOnPolicy(scopes, tool)
		if route.Policy != nil {
			route.From = RouteFromPolicy
		}
		out[identity] = route
	}
	return out, nil
}

// modelPin finds the explicit model block for an agent: the entry's own
// [subagents.<name>.<tool>], or any declaration or tune-only entry that
// resolves to the same catalog agent.
func (c *Config) modelPin(identity, entryName string, catalogKeyed bool, tool string) (ModelRoute, string) {
	if !catalogKeyed {
		return c.Subagents[entryName].ModelOverrideFor(tool), "subagents." + entryName + "." + tool
	}
	var found ModelRoute
	label := ""
	for _, name := range slices.Sorted(maps.Keys(c.Subagents)) {
		sa := c.Subagents[name]
		cat := name
		if !sa.IsTuneOnly() {
			var ok bool
			if cat, ok = SubagentCatalogName(sa.Source); !ok {
				continue
			}
		}
		if cat != identity {
			continue
		}
		if r := sa.ModelOverrideFor(tool); r.IsSet() {
			found, label = r, "subagents."+name+"."+tool
		}
	}
	return found, label
}

// agreeOnPolicy walks from the most specific level to the least and returns
// the first level where every scope resolves to the same policy.
func (c *Config) agreeOnPolicy(scopes []routeScope, tool string) (string, *RoutePolicy, string) {
	levels := []func(routeScope) routeCandidate{
		func(s routeScope) routeCandidate { return c.stageCandidate(s, tool) },
		func(s routeScope) routeCandidate {
			return c.packageCandidate(routeScope{kind: s.kind, pkg: s.pkg}, tool)
		},
		func(routeScope) routeCandidate { return c.defaultCandidate(tool) },
	}
	var notes []string
	for _, level := range levels {
		picks := make([]routeCandidate, len(scopes))
		found := false
		for i, s := range scopes {
			picks[i] = level(s)
			found = found || picks[i].policy != nil
		}
		if !found {
			continue
		}
		if agreed, selectors := samePolicy(picks); agreed {
			return strings.Join(selectors, ", "), picks[0].policy, strings.Join(notes, "; ")
		}
		notes = append(notes, describeSplit(scopes, picks))
	}
	return "", nil, strings.Join(notes, "; ")
}

// stageCandidate is the most specific policy for one scope.
func (c *Config) stageCandidate(s routeScope, tool string) routeCandidate {
	if s.kind == "engine" && s.stage != "" {
		if p := c.Routing.Engines[s.pkg].Stages[s.stage].For(tool); p != nil {
			return routeCandidate{"routing.engines." + s.pkg + ".stages." + s.stage + "." + tool, p}
		}
	}
	return c.packageCandidate(routeScope{kind: s.kind, pkg: s.pkg}, tool)
}

func (c *Config) packageCandidate(s routeScope, tool string) routeCandidate {
	switch s.kind {
	case "engine":
		if p := c.Routing.Engines[s.pkg].For(tool); p != nil {
			return routeCandidate{"routing.engines." + s.pkg + "." + tool, p}
		}
	case "workflow":
		if p := c.Routing.Workflows[s.pkg].For(tool); p != nil {
			return routeCandidate{"routing.workflows." + s.pkg + "." + tool, p}
		}
	}
	return c.defaultCandidate(tool)
}

func (c *Config) defaultCandidate(tool string) routeCandidate {
	if p := c.Routing.Default.For(tool); p != nil {
		return routeCandidate{"routing.default." + tool, p}
	}
	return routeCandidate{}
}

// samePolicy reports whether every pick carries an equal policy, and the
// distinct selectors that supplied it.
func samePolicy(picks []routeCandidate) (bool, []string) {
	var selectors []string
	for _, p := range picks {
		if p.policy == nil || !reflect.DeepEqual(*p.policy, *picks[0].policy) {
			return false, nil
		}
		if !slices.Contains(selectors, p.selector) {
			selectors = append(selectors, p.selector)
		}
	}
	return true, selectors
}

func describeSplit(scopes []routeScope, picks []routeCandidate) string {
	parts := make([]string, 0, len(scopes))
	for i, s := range scopes {
		where := "default"
		switch {
		case s.stage != "":
			where = s.pkg + "/" + s.stage
		case s.pkg != "":
			where = s.pkg
		}
		what := "no policy"
		if picks[i].selector != "" {
			what = picks[i].selector
		}
		parts = append(parts, where+" → "+what)
	}
	return "places this agent serves disagree (" + strings.Join(parts, ", ") + ")"
}
