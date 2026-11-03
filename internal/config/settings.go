package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/Novirmor/homonto/internal/catalog"
)

var processName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// SettingValue is one resolved package setting: the configured value, or
// the manifest default when the config sets none.
type SettingValue struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Value       any    `json:"value,omitempty"`
	Default     bool   `json:"default"` // true when Value is the manifest default (or unset)
	Description string `json:"description,omitempty"`
}

// PackageSettings is a package's resolved configuration.
type PackageSettings struct {
	Package   string                    `json:"package"`
	Table     string                    `json:"table"` // workflows | engines | services
	Settings  []SettingValue            `json:"settings"`
	Processes map[string]map[string]any `json:"processes,omitempty"`
}

// packageTables are the config tables whose entries may carry settings.
func (c *Config) packageTables() []struct {
	table     string
	resources map[string]Resource
} {
	return []struct {
		table     string
		resources map[string]Resource
	}{{"workflows", c.Workflows}, {"engines", c.Engines}, {"services", c.Services}}
}

// validatePackageSettings judges every declared package's settings and
// process definitions against its manifest schema (ADR 0095). A package not
// yet in the catalog (a remote root before the engine injects it) is
// skipped; ResolvePackages runs again once it is.
func (c *Config) validatePackageSettings(cl *catalog.Catalog) error {
	for _, group := range c.packageTables() {
		for _, name := range slices.Sorted(maps.Keys(group.resources)) {
			r := group.resources[name]
			fw, ok := c.packageFramework(cl, name)
			if !ok {
				continue
			}
			label := group.table + "." + name
			if err := validateSettingValues(label, fw, r.Settings); err != nil {
				return err
			}
			if err := validateProcesses(label, fw, r.Processes); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettingValues(label string, fw catalog.Framework, values map[string]any) error {
	if len(values) > 0 && len(fw.Settings) == 0 {
		return fmt.Errorf("parse config: %s.settings: %s declares no settings", label, packageDescription(fw))
	}
	return checkFields(label+".settings", fw.Settings, values)
}

func validateProcesses(label string, fw catalog.Framework, processes map[string]map[string]any) error {
	if len(processes) == 0 {
		return nil
	}
	if fw.Processes == nil {
		return fmt.Errorf("parse config: %s.processes: %s interprets no process definitions", label, packageDescription(fw))
	}
	for _, process := range slices.Sorted(maps.Keys(processes)) {
		if !processName.MatchString(process) {
			return fmt.Errorf("parse config: %s.processes: %q is not a process name (lowercase letters, digits, '_' or '-')", label, process)
		}
		if err := checkFields(label+".processes."+process, fw.Processes.Fields, processes[process]); err != nil {
			return err
		}
	}
	return nil
}

// checkFields validates values against schema: unknown keys named with the
// known ones, wrong types and enum misses named with the expected shape, and
// every required key present.
func checkFields(label string, schema map[string]catalog.SettingSchema, values map[string]any) error {
	known := slices.Sorted(maps.Keys(schema))
	for _, key := range slices.Sorted(maps.Keys(values)) {
		s, ok := schema[key]
		if !ok {
			return fmt.Errorf("parse config: %s.%s is not a known setting (known: %s)", label, key, strings.Join(known, ", "))
		}
		if err := s.Check(values[key]); err != nil {
			return fmt.Errorf("parse config: %s.%s: %w", label, key, err)
		}
	}
	for _, key := range known {
		if _, set := values[key]; schema[key].Required && !set {
			return fmt.Errorf("parse config: %s.%s is required (%s)", label, key, nonEmpty(schema[key].Description, schema[key].Type))
		}
	}
	return nil
}

func packageDescription(fw catalog.Framework) string {
	if fw.ManifestSchema < catalog.ContractManifestSchema {
		return fmt.Sprintf("package %q has a manifest_schema %d manifest, which", fw.Name, fw.ManifestSchema)
	}
	return fmt.Sprintf("package %q", fw.Name)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ResolvedSettings returns every declared package's settings with defaults
// filled in, sorted by package and setting name. Only packages whose
// manifest declares settings or processes appear.
func (c *Config) ResolvedSettings() ([]PackageSettings, error) {
	cl, err := c.FrameworkCatalog()
	if err != nil {
		return nil, err
	}
	var out []PackageSettings
	for _, group := range c.packageTables() {
		for _, name := range slices.Sorted(maps.Keys(group.resources)) {
			fw, ok := c.packageFramework(cl, name)
			if !ok || (len(fw.Settings) == 0 && fw.Processes == nil) {
				continue
			}
			r := group.resources[name]
			ps := PackageSettings{Package: name, Table: group.table, Settings: []SettingValue{}, Processes: r.Processes}
			for _, key := range slices.Sorted(maps.Keys(fw.Settings)) {
				s := fw.Settings[key]
				v, set := r.Settings[key]
				if !set {
					v = s.Default
				}
				ps.Settings = append(ps.Settings, SettingValue{Name: key, Type: s.Type, Value: v, Default: !set, Description: s.Description})
			}
			out = append(out, ps)
		}
	}
	slices.SortFunc(out, func(a, b PackageSettings) int { return strings.Compare(a.Package, b.Package) })
	return out, nil
}
