// Package workcli holds the scaffolding shared between the onto and to workflow
// CLIs: the framework-install gate every mutating command enforces, the change-
// name shape both validate against, and the doctor helpers (homonto-version
// readback, version normalization, the quiet-findings sentinel) that would
// otherwise drift between the two packages. Each package constructs a Framework
// value parameterized for its own name and reserved words; nothing in here reads
// or writes workflow-specific state.
package workcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ErrQuietFindings is what `<framework> doctor --quiet` returns when there are
// findings: the caller (cmd/<framework>/main.go) must exit non-zero WITHOUT
// printing — quiet mode's whole contract is "exit code only", so a hook
// capturing stderr sees nothing. ontocli.ErrQuietFindings and tocli.ErrQuietFindings
// alias this sentinel so each binary's main keeps its existing errors.Is check.
var ErrQuietFindings = errors.New("doctor: findings (quiet)")

// changeNamePattern is the accepted shape for a change name across both
// frameworks: one or more lowercase-alphanumeric segments joined by single
// hyphens. Compiled once and shared by every Framework.
var changeNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Framework names the workflow framework this helper set is parameterized for.
// ontocli and tocli each construct one instance to drive the shared scaffolding
// (the install gate and change-name validation) so the two stay in lockstep.
type Framework struct {
	// Name is the [frameworks.<name>] table key and the framework's own word:
	// "onto" or "to".
	Name string
	// SkillsDir is the catalog subdirectory whose presence proves the framework
	// was applied: "skills/onto" or "skills/to".
	SkillsDir string
	// GatePrefix is the error prefix the install gate uses. ontocli historically
	// emits "onto init" (the command gate was written for, even though many
	// commands now enforce it); tocli emits "to". Preserved verbatim so the
	// refactor changes no observable diagnostic.
	GatePrefix string
	// NamePrefix is the error prefix ValidChangeName uses: "onto new" or "to".
	NamePrefix string
	// ReservedNames are change names this framework refuses that the shape rule
	// alone would allow. tocli reserves "archive" (the archive directory itself);
	// ontocli reserves nothing here (its archive name conflict is structural).
	ReservedNames []string
}

// HomontoConfig is the minimal shape of homonto.toml the gate needs: just
// enough to detect whether a [frameworks.<name>] table is declared, plus the
// [repos] declarations for the multi-repo context lines (ADR 0024 stage 1).
// It is intentionally a standalone struct, not homonto's own config type, so
// each workflow CLI stays isolated from homonto's projection pipeline.
type HomontoConfig struct {
	Frameworks map[string]any    `toml:"frameworks"`
	Repos      map[string]string `toml:"repos"`
}

// DeclaredRepos reads the [repos] table from <root>/homonto.toml (nil when the
// file or table is absent). The workflow CLIs surface it as context only: the
// designated workflow tree stays in the config repo until the staged
// cross-repo work ships.
func DeclaredRepos(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "homonto.toml"))
	if err != nil {
		return nil
	}
	var c HomontoConfig
	if toml.Unmarshal(data, &c) != nil {
		return nil
	}
	return c.Repos
}

// RepoContextLines renders the declared-repos context block shared by onto
// init and to init: one header stating where the designated workflow tree
// lives and what the repos are, then one line per repo in name order. Empty
// (no lines) for a config with no [repos] table.
func RepoContextLines(root string) []string {
	repos := DeclaredRepos(root)
	if len(repos) == 0 {
		return nil
	}
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []string{
		"repos declared in homonto.toml — this workflow tree is the designated home; changes reach these repositories in a later stage:",
	}
	for _, name := range names {
		out = append(out, fmt.Sprintf("  %s  %s", name, repos[name]))
	}
	return out
}

// Gate enforces the framework-install precondition every mutating command in
// both frameworks requires: the project must have declared and applied
// [frameworks.<name>] through Homonto. The skills are the product — the gate
// guarantees no agent works inside the framework without them. It checks, in
// order, and returns on the first failure:
//
//  1. <root>/homonto.toml exists.
//  2. it declares a [frameworks.<name>] table.
//  3. <root>/.homonto/catalog/<skills-dir> exists as a directory (i.e. the
//     declaration has been applied).
//
// Gate performs no writes; it is safe to call before any scaffolding.
func (f Framework) Gate(root string) error {
	tomlPath := filepath.Join(root, "homonto.toml")

	data, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: no homonto.toml found in %s; run `homonto init` first", f.GatePrefix, root)
		}
		return fmt.Errorf("%s: reading %s: %w", f.GatePrefix, tomlPath, err)
	}

	var cfg HomontoConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("%s: parsing %s: %w", f.GatePrefix, tomlPath, err)
	}

	if _, ok := cfg.Frameworks[f.Name]; !ok {
		return fmt.Errorf("%s: %s has no [frameworks.%s] table; declare [frameworks.%s] and run `homonto apply`", f.GatePrefix, tomlPath, f.Name, f.Name)
	}

	catalogPath := filepath.Join(root, ".homonto", "catalog", f.SkillsDir)
	info, err := os.Stat(catalogPath)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s: %s not found; run `homonto apply` to install the %s framework", f.GatePrefix, catalogPath, f.Name)
	}

	return nil
}

// ValidChangeName rejects any change name that is empty, escapes its own base
// name (e.g. via ".." or a path separator), or does not match the lowercase-
// hyphenated shape both frameworks require for a change directory. It then
// rejects any name listed in the framework's ReservedNames (e.g. "archive" for
// to). The accepted/rejected set is identical across frameworks except for
// those reserved entries.
func (f Framework) ValidChangeName(name string) error {
	if name == "" {
		return fmt.Errorf("%s: change name must not be empty", f.NamePrefix)
	}
	if name != filepath.Base(name) || strings.Contains(name, "..") {
		return fmt.Errorf("%s: change name %q must not contain path separators or \"..\"", f.NamePrefix, name)
	}
	if !changeNamePattern.MatchString(name) {
		return fmt.Errorf("%s: change name %q must match %s", f.NamePrefix, name, changeNamePattern.String())
	}
	for _, reserved := range f.ReservedNames {
		if name == reserved {
			return fmt.Errorf("%s: change name %q is reserved", f.NamePrefix, name)
		}
	}
	return nil
}

// HomontoAppliedVersion reads the homonto version recorded by the last apply
// from <root>/.homonto/state.json ("" if unavailable). It deliberately reads
// only the homontoVersion field and imports none of homonto's projection
// packages, so each workflow CLI's doctor stays decoupled from the projection
// side.
func HomontoAppliedVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".homonto", "state.json"))
	if err != nil {
		return ""
	}
	var s struct {
		HomontoVersion string `json:"homontoVersion"`
	}
	if json.Unmarshal(data, &s) != nil {
		return ""
	}
	return s.HomontoVersion
}

// NormalizeVersion strips a leading "v" and any build metadata (from "+") so a
// dirty local build of two binaries compares equal on its release core. Used by
// both doctors' version-skew check.
func NormalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	return v
}
