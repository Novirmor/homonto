package catalog

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/Novirmor/homonto/internal/agentfm"
	"github.com/Novirmor/homonto/internal/capability"
	"gopkg.in/yaml.v3"
)

// Check is one conformance check's outcome. Err is nil when it passed.
type Check struct {
	Name string
	Err  error
}

// Conformance is a loaded package's conformance report (docs/contracts/).
// The manifest-level rules (kind, kind table, strict keys, resource paths,
// settings schema, required services) already ran when the catalog loaded
// the package; this adds the checks that need the package's content.
type Conformance struct {
	Package string
	Kind    string
	Version string
	// Contract is the kind's contract version the package declares (ADR 0108).
	Contract string
	Checks   []Check
}

// ConformanceSuiteContracts are the contract versions this suite verifies
// (ADR 0108). They must equal Contracts: a contract change that the suite was
// not updated for fails TestConformanceSuiteIsPinnedToTheContracts.
var ConformanceSuiteContracts = map[string]ContractVersion{
	KindEngine:   {1, 0},
	KindWorkflow: {1, 0},
	KindService:  {1, 0},
}

// Passed reports whether every check passed.
func (r Conformance) Passed() bool {
	for _, c := range r.Checks {
		if c.Err != nil {
			return false
		}
	}
	return true
}

// Conformance checks one loaded package against the public contract. The
// same function runs for builtin, local: and remote: packages, so the
// examples and the builtins are held to one standard.
func (c *Catalog) Conformance(name string) (Conformance, error) {
	fw, ok := c.frameworks[name]
	if !ok {
		return Conformance{}, fmt.Errorf("catalog: unknown package %q", name)
	}
	r := Conformance{Package: name, Kind: fw.Kind, Version: fw.Version}
	add := func(check string, err error) { r.Checks = append(r.Checks, Check{Name: check, Err: err}) }

	if fw.ManifestSchema < ContractManifestSchema {
		add("manifest", fmt.Errorf("manifest_schema %d carries no contract; set manifest_schema = %d with a kind and its table", fw.ManifestSchema, ContractManifestSchema))
		return r, nil
	}
	add("manifest", nil)
	r.Contract = fw.Contract.String()
	add("contract", checkSuiteContract(fw.Kind, fw.Contract))

	add("skills", c.checkSkills(fw))
	add("subagents", c.checkSubagents(fw))
	add("harness", capability.OpenCode().Check(name, fw.HarnessRequirements))
	return r, nil
}

// checkSuiteContract refuses to certify a package against a contract this
// suite does not verify.
func checkSuiteContract(kind string, v ContractVersion) error {
	suite, ok := ConformanceSuiteContracts[kind]
	if !ok || v.Major != suite.Major || v.Minor > suite.Minor {
		return fmt.Errorf("the conformance suite verifies %s contract %s, not %s", kind, suite, v)
	}
	return nil
}

// checkSkills requires every declared skill to hold a SKILL.md whose
// frontmatter names it, so the harness loads it under the declared name.
func (c *Catalog) checkSkills(fw Framework) error {
	for _, skill := range sortedKeys(fw.Skills) {
		fsys := c.skillFS[skill]
		data, err := fs.ReadFile(fsys, path.Join(c.skills[skill], "SKILL.md"))
		if err != nil {
			return fmt.Errorf("skill %q: %w", skill, err)
		}
		var header struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		fm, ok := frontmatter(data)
		if !ok {
			return fmt.Errorf("skill %q: SKILL.md has no --- frontmatter", skill)
		}
		if err := yaml.Unmarshal(fm, &header); err != nil {
			return fmt.Errorf("skill %q: SKILL.md frontmatter: %w", skill, err)
		}
		if header.Name != skill || header.Description == "" {
			return fmt.Errorf("skill %q: SKILL.md frontmatter needs name: %s and a description", skill, skill)
		}
	}
	return nil
}

// checkSubagents renders every intent agent with a placeholder model (the
// config supplies the real one) and validates every verbatim agent's name.
func (c *Catalog) checkSubagents(fw Framework) error {
	for _, agent := range sortedKeys(fw.Subagents) {
		data, ok, err := c.SubagentContent(agent)
		if err != nil || !ok {
			return fmt.Errorf("subagent %q: unreadable: %v", agent, err)
		}
		intent, err := agentfm.NeedsTransform(data)
		if err != nil {
			return fmt.Errorf("subagent %q: %w", agent, err)
		}
		if !intent {
			if err := agentfm.ValidateInstalledName(agent, data); err != nil {
				return fmt.Errorf("subagent %q: %w", agent, err)
			}
			continue
		}
		ctx := &agentfm.RenderContext{Overrides: map[string]agentfm.ModelSpec{agent: {Model: "conformance/check"}}}
		if _, err := agentfm.Render(agent, data, "opencode", ctx); err != nil {
			return fmt.Errorf("subagent %q: %w", agent, err)
		}
	}
	return nil
}

func frontmatter(data []byte) ([]byte, bool) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, false
	}
	end := bytes.Index(data[4:], []byte("\n---"))
	if end < 0 {
		return nil, false
	}
	return data[4 : 4+end], true
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PackageNames lists every indexed package, sorted.
func (c *Catalog) PackageNames() []string {
	out := make([]string, 0, len(c.frameworks))
	for name := range c.frameworks {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
