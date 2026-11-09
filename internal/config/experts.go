package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/remote"
)

// Expert installs a package, or references an existing native agent. External
// references never project, tune or take ownership of the native agent/skill.
type Expert struct {
	Resource
	Description string `toml:"description"`
	Skill       string `toml:"skill"`
	Reviewer    string `toml:"reviewer"`
}

func (e Expert) External() bool { return strings.HasPrefix(e.Source, "external:") }

func expertSupport(kind string) []string {
	switch kind {
	case "skill":
		return []string{"expert", "homonto"}
	case "subagent":
		return []string{"homonto"}
	case "command":
		return []string{"expert"}
	}
	return nil
}

func sortedExpertNames(experts map[string]Expert) []string {
	names := make([]string, 0, len(experts))
	for name := range experts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Config) ExpertPackages() map[string]Resource {
	packages := map[string]Resource{}
	for name, expert := range c.Experts {
		if !expert.External() {
			packages[name] = expert.Resource
		}
	}
	return packages
}

func (c *Config) validateExperts() error {
	names := make([]string, 0, len(c.Experts))
	for name := range c.Experts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := c.Experts[name]
		if _, ok := c.Workflows[name]; ok {
			return fmt.Errorf("parse config: %q is declared as both an expert and a workflow", name)
		}
		if _, ok := c.Engines[name]; ok {
			return fmt.Errorf("parse config: %q is declared as both an expert and an engine", name)
		}
		if e.Repo != "" {
			return fmt.Errorf("parse config: experts.%s: experts install at the config root; repo is not supported", name)
		}
		if !e.External() {
			if e.Description != "" || e.Skill != "" || e.Reviewer != "" {
				return fmt.Errorf("parse config: experts.%s: description, skill and reviewer belong to external: references; managed packages declare these in framework.toml", name)
			}
			continue
		}
		// Reuse placement validation; external references have no digest or files.
		r := e.Resource
		r.Source = "builtin:external-reference"
		if err := validateFrameworkResources("experts", map[string]Resource{name: r}); err != nil {
			return err
		}
		if strings.TrimSpace(e.Description) == "" || strings.ContainsFunc(e.Description, unicode.IsControl) {
			return fmt.Errorf("parse config: experts.%s: external reference needs a single-line description", name)
		}
		for _, field := range []struct{ label, id string }{{"agent", strings.TrimPrefix(e.Source, "external:")}, {"skill", e.Skill}, {"reviewer", e.Reviewer}} {
			label, id := field.label, field.id
			if id == "" && label != "agent" {
				continue
			}
			if !validExternalID(id) {
				return fmt.Errorf("parse config: experts.%s: invalid external %s ID %q", name, label, id)
			}
			if label != "skill" && id == "homonto" {
				return fmt.Errorf("parse config: experts.%s: the coordinator cannot be its own expert", name)
			}
		}
	}
	return validateFrameworkResources("experts", c.ExpertPackages())
}

func validExternalID(id string) bool {
	if id == "" {
		return false
	}
	for _, part := range strings.Split(id, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return false
			}
		}
	}
	return true
}

// Remote expert membership cannot be checked until the verified manifest is
// available. Route syntax is still validated during the initial config load.
func (c *Config) unresolvedRemoteExperts() bool {
	for name, expert := range c.Experts {
		if remote.IsRemoteSource(expert.Source) && c.remoteFrameworkDirs[name] == "" {
			return true
		}
	}
	return false
}

// ValidateExpertInstallation runs after remote packages have been resolved, so
// missing resources or model routes fail before any plan/apply side effects.
func (c *Config) ValidateExpertInstallation() error {
	if len(c.Experts) == 0 {
		return nil
	}
	if _, err := c.ResolvedExperts("opencode"); err != nil {
		return err
	}
	if _, err := c.ExpandedSubagentEntriesForTool("opencode"); err != nil {
		return err
	}
	return validateModels(c)
}

// ResolvedExperts is the installed inventory, not every available catalog expert.
// It is also the input to coordinator dispatch permissions and render fingerprints.
func (c *Config) ResolvedExperts(tool string) ([]catalog.ExpertDescriptor, error) {
	if len(c.Experts) == 0 {
		return nil, nil
	}
	cl, err := c.FrameworkCatalog()
	if err != nil {
		return nil, err
	}
	var out []catalog.ExpertDescriptor
	for name, e := range c.Experts {
		if !slices.Contains(e.TargetsOrAll(), tool) {
			continue
		}
		d := catalog.ExpertDescriptor{Name: name, Source: e.Source, External: e.External()}
		if remote.IsRemoteSource(d.Source) {
			d.Source = remote.RedactLocator(d.Source)
		}
		if e.External() {
			d.Description, d.Skill, d.Analyst, d.Reviewer = e.Description, e.Skill, strings.TrimPrefix(e.Source, "external:"), e.Reviewer
		} else {
			key, _ := FrameworkCatalogName(name, e.Source)
			fw, ok := cl.Framework(key)
			if !ok || fw.Kind != "expert" || fw.Expert == nil {
				return nil, fmt.Errorf("experts.%s: %q is not a valid expert package", name, e.Source)
			}
			d.Description, d.Skill, d.Analyst, d.Reviewer = fw.Description, fw.Expert.Skill, fw.Expert.Analyst, fw.Expert.Reviewer
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
