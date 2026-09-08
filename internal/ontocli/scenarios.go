package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

type scenarioDeclaration struct {
	Path        string
	Line        int
	Name        string
	Requirement string
}

type scenarioIndex map[string][]scenarioDeclaration

var scenarioIDLine = regexp.MustCompile(`^Scenario-ID:[\t ]*([^\s<>]+)[\t ]*$`)
var requirementIDLine = regexp.MustCompile(`^Requirement-ID:[\t ]*([^\s<>]+)[\t ]*$`)

// Index declarations, not prose references or fenced examples. Deltas own their
// scenario contract; only no-spec presets may declare IDs in tasks/verification.
// Each ID has one canonical declaration site, including across those two files.
func loadScenarioIndex(changeDir string, st ontostate.State) (scenarioIndex, error) {
	index := scenarioIndex{}
	paths, err := deltaSpecPaths(filepath.Join(changeDir, "specs"))
	if err != nil {
		return index, err
	}
	preset := len(paths) == 0 && (st.Workflow == "fix" || st.Workflow == "tweak")
	if preset {
		paths = []string{filepath.Join(changeDir, "tasks.md"), filepath.Join(changeDir, "verification.md")}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if preset && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return index, err
		}
		rel, err := filepath.Rel(changeDir, path)
		if err != nil {
			return index, err
		}
		var requirement, scenario, fence string
		for i, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
				n := len(line) - len(strings.TrimLeft(line, line[:1]))
				if fence == "" {
					fence = line[:n]
				} else if strings.HasPrefix(line, fence) && strings.TrimSpace(line[n:]) == "" {
					fence = ""
				}
				continue
			}
			if fence != "" {
				continue
			}
			if !preset {
				switch {
				case strings.HasPrefix(line, "### Requirement:"):
					requirement = strings.TrimSpace(strings.TrimPrefix(line, "### Requirement:"))
					scenario = ""
				case strings.HasPrefix(line, "#### Scenario:"):
					scenario = strings.TrimSpace(strings.TrimPrefix(line, "#### Scenario:"))
				case strings.HasPrefix(line, "# "), strings.HasPrefix(line, "## "), strings.HasPrefix(line, "### "):
					requirement, scenario = "", ""
				case strings.HasPrefix(line, "#### "):
					scenario = ""
				}
				if match := requirementIDLine.FindStringSubmatch(line); match != nil && requirement != "" && scenario == "" {
					requirement = match[1]
				}
				if requirement == "" || scenario == "" {
					continue
				}
			}
			if match := scenarioIDLine.FindStringSubmatch(line); match != nil {
				index[match[1]] = append(index[match[1]], scenarioDeclaration{
					Path: filepath.ToSlash(rel), Line: i + 1, Name: scenario, Requirement: requirement,
				})
			}
		}
	}
	return index, nil
}

func scenarioAmbiguity(id string, declarations []scenarioDeclaration) string {
	if len(declarations) < 2 {
		return ""
	}
	var sites []string
	for _, declaration := range declarations {
		sites = append(sites, fmt.Sprintf("%s:%d", declaration.Path, declaration.Line))
	}
	return fmt.Sprintf("duplicate Scenario-ID %q declared at %s; keep one canonical declaration per ID and use plain references elsewhere", id, strings.Join(sites, ", "))
}

func scenarioFindings(name string, index scenarioIndex) []string {
	var findings []string
	for id, declarations := range index {
		if finding := scenarioAmbiguity(id, declarations); finding != "" {
			findings = append(findings, name+": "+finding)
		}
	}
	sort.Strings(findings)
	return findings
}
