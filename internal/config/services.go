package config

import (
	"fmt"
	"maps"
	"slices"

	"github.com/Novirmor/homonto/internal/catalog"
)

// ResolvePackages settles the package set once the catalog can read every
// package: which service package implements each service a declared package
// requires (ADR 0094), and whether each package's settings and process
// definitions fit the schema its manifest declares (ADR 0095).
//
// A service the config declares under [services.*] wins for its id;
// otherwise the requiring package's default service package is installed
// beside it, with the requirer's scope and targets. Two
// installed packages implementing one service id is an error: the runtime
// and the agents would not know whose operations to use.
//
// Load runs it once the config's local roots resolve; the engine runs it
// again after injecting verified remote package roots, whose requirements
// are only known then.
func (c *Config) ResolvePackages() error {
	for name := range c.impliedServices {
		delete(c.Frameworks, name)
	}
	c.impliedServices = nil
	if len(c.Frameworks) == 0 {
		return nil
	}
	cl, err := c.FrameworkCatalog()
	if err != nil {
		return err
	}
	implementedBy := map[string]string{}
	names := slices.Sorted(maps.Keys(c.Frameworks))
	for _, name := range names {
		fw, ok := c.packageFramework(cl, name)
		if !ok || fw.Service == nil {
			continue
		}
		if prev, dup := implementedBy[fw.Service.ID]; dup {
			return fmt.Errorf("parse config: services.%s and services.%s both implement service %q; declare one", prev, name, fw.Service.ID)
		}
		implementedBy[fw.Service.ID] = name
	}
	for _, name := range names {
		fw, ok := c.packageFramework(cl, name)
		if !ok {
			continue
		}
		requirer := c.Frameworks[name]
		for _, id := range slices.Sorted(maps.Keys(fw.RequiredServices)) {
			if _, ok := implementedBy[id]; ok {
				continue
			}
			def := fw.RequiredServices[id]
			if _, taken := c.Frameworks[def]; taken {
				return fmt.Errorf("parse config: %s requires service %q from default package %q, but %q is already declared for something else", name, id, def, def)
			}
			if c.impliedServices == nil {
				c.impliedServices = map[string]string{}
			}
			c.Frameworks[def] = Resource{Source: "builtin:" + def, Scope: requirer.Scope, Targets: requirer.Targets}
			c.impliedServices[def] = name
			implementedBy[id] = def
		}
	}
	return c.validatePackageSettings(cl)
}

// ImpliedServices maps each service package installed only as a default to
// the package that required it.
func (c *Config) ImpliedServices() map[string]string {
	return maps.Clone(c.impliedServices)
}

// packageFramework looks up the catalog package a declaration expands.
func (c *Config) packageFramework(cl *catalog.Catalog, name string) (catalog.Framework, bool) {
	catName, ok := FrameworkCatalogName(name, c.Frameworks[name].Source)
	if !ok {
		return catalog.Framework{}, false
	}
	return cl.Framework(catName)
}

// InstalledServices returns the catalog contract of every installed service
// package (declared or implied), keyed by its config name.
func (c *Config) InstalledServices() map[string]catalog.Framework {
	out := map[string]catalog.Framework{}
	if len(c.Frameworks) == 0 {
		return out
	}
	cl, err := c.FrameworkCatalog()
	if err != nil {
		return out // validation already reported the catalog error
	}
	for name := range c.Frameworks {
		if fw, ok := c.packageFramework(cl, name); ok && fw.Service != nil {
			out[name] = fw
		}
	}
	return out
}

// HostingProviders reports the draft/publication runtime providers enabled
// by the installed service packages (ADR 0090, ADR 0094).
func (c *Config) HostingProviders() map[string]bool {
	out := map[string]bool{}
	for _, fw := range c.InstalledServices() {
		if fw.Service.Runtime != "" {
			out[fw.Service.Runtime] = true
		}
	}
	return out
}

// ServiceRules is the union of the shell rules installed services contribute
// to rendered agents, deduplicated and sorted.
type ServiceRules struct {
	CoordinatorAsk  []string
	CoordinatorDeny []string
	WorkerDeny      []string
}

// InstalledServiceRules collects every installed service's permission rules.
func (c *Config) InstalledServiceRules() ServiceRules {
	var r ServiceRules
	for _, fw := range c.InstalledServices() {
		r.CoordinatorAsk = append(r.CoordinatorAsk, fw.Permissions.Coordinator.BashAsk...)
		r.CoordinatorDeny = append(r.CoordinatorDeny, fw.Permissions.Coordinator.BashDeny...)
		r.WorkerDeny = append(r.WorkerDeny, fw.Permissions.Workers.BashDeny...)
	}
	for _, list := range []*[]string{&r.CoordinatorAsk, &r.CoordinatorDeny, &r.WorkerDeny} {
		slices.Sort(*list)
		*list = slices.Compact(*list)
	}
	return r
}
