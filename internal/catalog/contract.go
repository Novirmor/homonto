package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/Novirmor/homonto/internal/capability"
	toml "github.com/pelletier/go-toml/v2"
)

// ContractManifestSchema is the manifest_schema that carries the public
// package contract (docs/contracts/, ADR 0092): a required kind, the
// kind-specific [engine]/[workflow]/[service] table, harness requirements and
// strict decoding. Schema 0/1 manifests still load as plain resource bundles
// but carry no contract, so nothing contract-driven (engine selection,
// services, settings) can use them.
const ContractManifestSchema = 2

// The package kinds.
const (
	KindEngine   = "engine"
	KindWorkflow = "workflow"
	KindService  = "service"
)

// The shared coordinator (ADR 0045): one primary agent, its knowledge skill,
// and its runtime plugin. A schema 2 package never declares these itself; it
// sets coordinator = "shared" and the catalog adds them from the base catalog.
const (
	SharedCoordinatorAgent  = "homonto"
	SharedCoordinatorSkill  = SharedKnowledgeSkill
	SharedCoordinatorPlugin = "homonto-workflow"
	coordinatorShared       = "shared"
)

// Engine lifecycle ownership (ADR 0093).
const (
	// LifecycleStateless engines own no durable state; a workspace task keeps
	// only the engine name (today: no).
	LifecycleStateless = "stateless"
	// LifecycleNative engines own a lifecycle record through their companion
	// binary, and bind source checkouts through homonto's worktree registry
	// keyed by the engine name (today: onto, to).
	LifecycleNative = "native"
)

// Engine task operations a coordinator may run against a workspace task.
const (
	OpStart            = "start"
	OpResume           = "resume"
	OpIntegrationCheck = "integration-check"
	OpRelease          = "release"
)

var engineOperations = []string{OpStart, OpResume, OpIntegrationCheck, OpRelease}

// Service operations (ADR 0094).
const (
	ServiceRead    = "read"
	ServiceDraft   = "draft"
	ServicePublish = "publish"
)

var serviceOperations = []string{ServiceRead, ServiceDraft, ServicePublish}

// RuntimeProviders are the host providers built into the homonto binary's
// draft/publication runtime (ADR 0090). A service may enable one; it cannot
// add a new one, because providers are Go code.
var RuntimeProviders = []string{"github", "gitlab", "bitbucket"}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// EngineContract is the [engine] table of a schema 2 engine.
type EngineContract struct {
	Lifecycle   string   `toml:"lifecycle"`
	Binary      string   `toml:"binary"`
	Entry       []string `toml:"entry"`
	Operations  []string `toml:"operations"`
	Stages      []string `toml:"stages"`
	Coordinator string   `toml:"coordinator"`
	// AgentStages binds the engine's own subagents to the stages they serve
	// ([engine.agent_stages], ADR 0099). A stage routing policy reaches an
	// agent only through this binding; an unbound agent is routed by the
	// engine-level policy.
	AgentStages map[string][]string `toml:"agent_stages"`
}

// StagesOf returns the stages agent is bound to, nil when it is unbound.
func (e EngineContract) StagesOf(agent string) []string { return e.AgentStages[agent] }

// Supports reports whether the engine declares operation op.
func (e EngineContract) Supports(op string) bool { return slices.Contains(e.Operations, op) }

// OwnsLifecycle reports whether the engine owns durable lifecycle state.
func (e EngineContract) OwnsLifecycle() bool { return e.Lifecycle == LifecycleNative }

// WorkflowContract is the [workflow] table of a schema 2 workflow.
type WorkflowContract struct {
	Coordinator string   `toml:"coordinator"`
	Entry       []string `toml:"entry"`
}

// Credential is one credential a service needs before it can operate.
type Credential struct {
	Kind  string `toml:"kind"`  // "cli" (a logged-in CLI) or "env" (an environment variable)
	Check string `toml:"check"` // cli: the read-only command that proves the login
	Name  string `toml:"name"`  // env: the variable name; never its value
}

// ServiceContract is the [service] table of a schema 2 service.
type ServiceContract struct {
	ID          string       `toml:"id"`
	Runtime     string       `toml:"runtime"`
	Operations  []string     `toml:"operations"`
	Transport   string       `toml:"transport"`
	Credentials []Credential `toml:"credentials"`
}

// PermissionRules are the shell rules a service contributes to rendered
// agents. They only tighten (ADR 0094): the coordinator may gain asks and
// denies, workers only denies, and nothing grants an allow.
type PermissionRules struct {
	Coordinator struct {
		BashAsk  []string `toml:"bash_ask"`
		BashDeny []string `toml:"bash_deny"`
	} `toml:"coordinator"`
	Workers struct {
		BashDeny []string `toml:"bash_deny"`
	} `toml:"workers"`
}

// Empty reports whether no rule is contributed.
func (p PermissionRules) Empty() bool {
	return len(p.Coordinator.BashAsk) == 0 && len(p.Coordinator.BashDeny) == 0 && len(p.Workers.BashDeny) == 0
}

type requiresTOML struct {
	Harness  []string          `toml:"harness"`
	Services map[string]string `toml:"services"`
}

// decodeManifest parses a framework.toml. A schema 2 manifest is decoded
// strictly so a misspelled contract key fails instead of silently dropping;
// older manifests keep the lax decode they always had.
func decodeManifest(b []byte) (frameworkTOML, error) {
	var ft frameworkTOML
	if err := toml.Unmarshal(b, &ft); err != nil {
		return ft, err
	}
	if ft.ManifestSchema < ContractManifestSchema {
		return ft, nil
	}
	ft = frameworkTOML{}
	if err := toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&ft); err != nil {
		var missing *toml.StrictMissingError
		if errors.As(err, &missing) {
			return ft, fmt.Errorf("unknown manifest fields:\n%s", missing.String())
		}
		return ft, err
	}
	return ft, nil
}

// validateContract checks a schema 2 manifest's kind-specific contract. It
// runs before any resource is indexed. name labels errors.
func validateContract(name string, ft frameworkTOML) error {
	if ft.ManifestSchema < ContractManifestSchema {
		// The lax legacy decode would otherwise accept a settings schema
		// nothing validates.
		if len(ft.Settings) > 0 || ft.Processes != nil {
			return fmt.Errorf("catalog: package %q declares [settings] or [processes]; set manifest_schema = 2 (docs/contracts/settings.md)", name)
		}
		return nil
	}
	label := fmt.Sprintf("catalog: package %q", name)
	if ft.Version == "" {
		return fmt.Errorf("%s: version is required", label)
	}
	if err := capability.ValidateNames(label+" [requires].harness", ft.Requires.Harness); err != nil {
		return err
	}
	for service, def := range ft.Requires.Services {
		if !identifier.MatchString(service) {
			return fmt.Errorf("%s [requires.services]: %q is not a service id (lowercase letters, digits, '-')", label, service)
		}
		if def == "" {
			return fmt.Errorf("%s [requires.services].%s names no default service package", label, service)
		}
	}
	for kind, declared := range map[string]bool{KindEngine: ft.Engine != nil, KindWorkflow: ft.Workflow != nil, KindService: ft.Service != nil} {
		if declared && kind != ft.Kind {
			return fmt.Errorf("%s is kind %q but declares an [%s] table", label, ft.Kind, kind)
		}
	}
	for _, reserved := range []struct {
		kind, name string
		has        bool
	}{
		{"subagent", SharedCoordinatorAgent, ft.Subagents[SharedCoordinatorAgent] != ""},
		{"skill", SharedCoordinatorSkill, ft.Skills[SharedCoordinatorSkill] != ""},
		{"plugin", SharedCoordinatorPlugin, ft.Plugins[SharedCoordinatorPlugin] != ""},
	} {
		if reserved.has {
			return fmt.Errorf("%s declares the shared coordinator's %s %q; set coordinator = \"shared\" in its [%s] table instead (ADR 0092)", label, reserved.kind, reserved.name, ft.Kind)
		}
	}
	if err := validateSettingsSchema(label, ft.Settings, ft.Processes); err != nil {
		return err
	}
	if !ft.Permissions.Empty() && ft.Kind != KindService {
		return fmt.Errorf("%s: only a service may contribute [permissions]", label)
	}
	if _, err := checkContractVersion(label, ft.Kind, ft.Compat.Contract); err != nil {
		return err
	}
	switch ft.Kind {
	case KindEngine:
		if ft.Engine == nil {
			return fmt.Errorf("%s is an engine but has no [engine] table", label)
		}
		return validateEngine(label, ft)
	case KindWorkflow:
		if ft.Workflow == nil {
			return fmt.Errorf("%s is a workflow but has no [workflow] table", label)
		}
		return validateWorkflow(label, ft)
	case KindService:
		if ft.Service == nil {
			return fmt.Errorf("%s is a service but has no [service] table", label)
		}
		return validateService(label, ft)
	case "":
		return fmt.Errorf("%s: kind is required (engine, workflow or service)", label)
	default:
		return fmt.Errorf("%s: unknown kind %q (engine, workflow or service)", label, ft.Kind)
	}
}

func validateCoordinator(label, value string) error {
	if value != "" && value != coordinatorShared {
		return fmt.Errorf("%s: coordinator %q is not supported; the only coordinator is the shared one (coordinator = \"shared\", ADR 0092)", label, value)
	}
	return nil
}

func validateEngine(label string, ft frameworkTOML) error {
	e := ft.Engine
	switch e.Lifecycle {
	case LifecycleStateless:
		if e.Binary != "" {
			return fmt.Errorf("%s [engine]: a stateless engine ships no lifecycle binary; drop binary or declare lifecycle = \"native\"", label)
		}
	case LifecycleNative:
		if e.Binary == "" {
			return fmt.Errorf("%s [engine]: lifecycle = \"native\" requires the binary that owns the lifecycle", label)
		}
		if !identifier.MatchString(e.Binary) {
			return fmt.Errorf("%s [engine]: binary %q is not a plain command name", label, e.Binary)
		}
	case "":
		return fmt.Errorf("%s [engine]: lifecycle is required (stateless or native)", label)
	default:
		return fmt.Errorf("%s [engine]: unknown lifecycle %q (stateless or native)", label, e.Lifecycle)
	}
	if len(e.Entry) == 0 {
		return fmt.Errorf("%s [engine]: entry must name at least one entry skill", label)
	}
	for _, skill := range e.Entry {
		if ft.Skills[skill] == "" {
			return fmt.Errorf("%s [engine]: entry skill %q is not declared in [skills]", label, skill)
		}
	}
	if err := validateSubset(label+" [engine].operations", e.Operations, engineOperations); err != nil {
		return err
	}
	for _, required := range []string{OpStart, OpResume} {
		if !e.Supports(required) {
			return fmt.Errorf("%s [engine].operations must include %q", label, required)
		}
	}
	if len(e.Stages) == 0 {
		return fmt.Errorf("%s [engine]: stages must name at least one stage (model-routing policies address them, ADR 0098)", label)
	}
	seen := map[string]bool{}
	for _, stage := range e.Stages {
		if !identifier.MatchString(stage) {
			return fmt.Errorf("%s [engine]: stage %q is not an identifier (lowercase letters, digits, '-')", label, stage)
		}
		if seen[stage] {
			return fmt.Errorf("%s [engine]: stage %q is listed twice", label, stage)
		}
		seen[stage] = true
	}
	for _, agent := range slices.Sorted(maps.Keys(e.AgentStages)) {
		if ft.Subagents[agent] == "" {
			return fmt.Errorf("%s [engine.agent_stages]: %q is not a subagent declared in [subagents]", label, agent)
		}
		bound := e.AgentStages[agent]
		if len(bound) == 0 {
			return fmt.Errorf("%s [engine.agent_stages]: %s must name at least one stage", label, agent)
		}
		dup := map[string]bool{}
		for _, stage := range bound {
			if !seen[stage] {
				return fmt.Errorf("%s [engine.agent_stages]: %s names stage %q, which is not in [engine].stages (%s)", label, agent, stage, strings.Join(e.Stages, ", "))
			}
			if dup[stage] {
				return fmt.Errorf("%s [engine.agent_stages]: %s lists stage %q twice", label, agent, stage)
			}
			dup[stage] = true
		}
	}
	return validateCoordinator(label+" [engine]", e.Coordinator)
}

func validateWorkflow(label string, ft frameworkTOML) error {
	w := ft.Workflow
	if len(w.Entry) == 0 {
		return fmt.Errorf("%s [workflow]: entry must name at least one entry command", label)
	}
	for _, command := range w.Entry {
		if ft.Commands[command] == "" {
			return fmt.Errorf("%s [workflow]: entry command %q is not declared in [commands]", label, command)
		}
	}
	return validateCoordinator(label+" [workflow]", w.Coordinator)
}

func validateService(label string, ft frameworkTOML) error {
	s := ft.Service
	if !identifier.MatchString(s.ID) {
		return fmt.Errorf("%s [service]: id %q is not a service id (lowercase letters, digits, '-')", label, s.ID)
	}
	if len(ft.Requires.Services) > 0 {
		return fmt.Errorf("%s: a service cannot require another service", label)
	}
	if len(ft.Subagents) > 0 || len(ft.Plugins) > 0 {
		return fmt.Errorf("%s: a service ships no subagents or plugins; it contributes operations, credentials and permission rules (ADR 0094)", label)
	}
	if s.Runtime != "" && !slices.Contains(RuntimeProviders, s.Runtime) {
		return fmt.Errorf("%s [service]: runtime %q is not a provider built into this homonto (%s)", label, s.Runtime, strings.Join(RuntimeProviders, ", "))
	}
	if len(s.Operations) == 0 {
		return fmt.Errorf("%s [service]: operations must name at least one of %s", label, strings.Join(serviceOperations, ", "))
	}
	if err := validateSubset(label+" [service].operations", s.Operations, serviceOperations); err != nil {
		return err
	}
	if s.Runtime != "" {
		// The built-in runtime exposes the complete draft/publication lifecycle;
		// allowing a service to advertise only (for example) "read" would still
		// register its publish-capable tools and make the contract misleading.
		for _, operation := range serviceOperations {
			if !slices.Contains(s.Operations, operation) {
				return fmt.Errorf("%s [service]: runtime %q exposes the draft/publication lifecycle; operations must include %q", label, s.Runtime, operation)
			}
		}
	}
	if slices.Contains(s.Operations, ServiceDraft) && s.Runtime == "" {
		return fmt.Errorf("%s [service]: the %q operation is served by the binary's draft runtime; declare runtime", label, ServiceDraft)
	}
	if strings.TrimSpace(s.Transport) == "" {
		return fmt.Errorf("%s [service]: transport must name the CLI or command the service operates through", label)
	}
	for i, c := range s.Credentials {
		switch c.Kind {
		case "cli":
			if strings.TrimSpace(c.Check) == "" {
				return fmt.Errorf("%s [service].credentials[%d]: a cli credential needs the read-only check command", label, i)
			}
		case "env":
			if !envName.MatchString(c.Name) {
				return fmt.Errorf("%s [service].credentials[%d]: env credential name %q is not an environment variable name", label, i, c.Name)
			}
		default:
			return fmt.Errorf("%s [service].credentials[%d]: kind %q must be cli or env", label, i, c.Kind)
		}
	}
	for _, group := range []struct {
		label string
		rules []string
	}{
		{"[permissions.coordinator].bash_ask", ft.Permissions.Coordinator.BashAsk},
		{"[permissions.coordinator].bash_deny", ft.Permissions.Coordinator.BashDeny},
		{"[permissions.workers].bash_deny", ft.Permissions.Workers.BashDeny},
	} {
		for _, rule := range group.rules {
			if strings.TrimSpace(rule) == "" || strings.ContainsAny(rule, "\n\r") {
				return fmt.Errorf("%s %s: rule %q must be a non-empty single-line pattern", label, group.label, rule)
			}
		}
	}
	return nil
}

func validateSubset(label string, values, allowed []string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if !slices.Contains(allowed, v) {
			return fmt.Errorf("%s: unknown value %q (allowed: %s)", label, v, strings.Join(allowed, ", "))
		}
		if seen[v] {
			return fmt.Errorf("%s: %q is listed twice", label, v)
		}
		seen[v] = true
	}
	return nil
}

// usesSharedCoordinator reports whether a manifest asks for the shared
// coordinator.
func usesSharedCoordinator(ft frameworkTOML) bool {
	return (ft.Engine != nil && ft.Engine.Coordinator == coordinatorShared) ||
		(ft.Workflow != nil && ft.Workflow.Coordinator == coordinatorShared)
}

// validateSharedCoordinator runs after every source is merged: a package that
// asked for the shared coordinator needs the base catalog to provide it.
func (c *Catalog) validateSharedCoordinator() error {
	for name, fw := range c.frameworks {
		if !fw.SharedCoordinator {
			continue
		}
		_, agent := c.subagents[SharedCoordinatorAgent]
		_, skill := c.skills[SharedCoordinatorSkill]
		_, plugin := c.plugins[SharedCoordinatorPlugin]
		if !agent || !skill || !plugin {
			return fmt.Errorf("catalog: package %q uses the shared coordinator, but this catalog does not provide it", name)
		}
	}
	return nil
}
