package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// IntegrationCheck runs against the selected checkouts, never inferred sibling paths.
type IntegrationCheck struct {
	Repos   []string `toml:"repos" json:"repos"`
	Repo    string   `toml:"repo" json:"repo"`
	Cwd     string   `toml:"cwd" json:"cwd"`
	Command []string `toml:"command" json:"command"`
}

func validateIntegration(c *Config) error {
	for _, name := range sortedKeysOfIntegration(c.Integration) {
		check := c.Integration[name]
		label := "integration." + name
		if err := validateResourceName("integration", name); err != nil {
			return err
		}
		if c.SchemaVersion != 2 {
			return fmt.Errorf("%s requires schema_version = 2 and explicit [repos]", label)
		}
		seen := map[string]bool{}
		for _, repo := range check.Repos {
			if _, ok := c.Repos[repo]; !ok || seen[repo] {
				return fmt.Errorf("%s: repository %q is undeclared or duplicated", label, repo)
			}
			seen[repo] = true
		}
		if !seen[check.Repo] {
			return fmt.Errorf("%s: execution repo must be a participating repository", label)
		}
		cwd := filepath.Clean(check.Cwd)
		if check.Cwd == "" || filepath.IsAbs(cwd) || cwd == ".." || strings.HasPrefix(cwd, ".."+string(filepath.Separator)) || strings.ContainsAny(check.Cwd, "\\\x00\r\n") {
			return fmt.Errorf("%s: cwd must be relative to the execution checkout (use . for its root)", label)
		}
		if len(check.Command) == 0 || strings.TrimSpace(check.Command[0]) == "" {
			return fmt.Errorf("%s: command must be a nonempty argument array", label)
		}
		for _, arg := range check.Command {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("%s: command contains NUL", label)
			}
		}
	}
	return nil
}

func sortedKeysOfIntegration(checks map[string]IntegrationCheck) []string {
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// IntegrationChecksFor selects checks by ANY changed participant, in stable order.
func (c *Config) IntegrationChecksFor(changed []string) []string {
	var names []string
	for _, name := range sortedKeysOfIntegration(c.Integration) {
		for _, repo := range c.Integration[name].Repos {
			if slices.Contains(changed, repo) {
				names = append(names, name)
				break
			}
		}
	}
	return names
}
