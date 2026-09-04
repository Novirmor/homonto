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
	Workflow   struct {
		Root string `toml:"root"`
	} `toml:"workflow"`
}

// WorkflowRoot resolves the configured workflow root beneath root. An omitted
// setting preserves the historic docs/ layout. It intentionally reads only the
// small configuration surface workflow CLIs require.
func WorkflowRoot(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "homonto.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			workflowRoot := filepath.Join(root, "docs")
			if err := ValidateWorkflowPath(root, workflowRoot); err != nil {
				return "", err
			}
			return workflowRoot, nil
		}
		return "", err
	}
	var cfg HomontoConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	rel := strings.TrimSpace(cfg.Workflow.Root)
	if rel == "" {
		rel = "docs"
	}
	if filepath.IsAbs(rel) || rel == "." || strings.Contains(rel, `\`) {
		return "", fmt.Errorf("workflow.root %q must be a relative path below the configuration repository", cfg.Workflow.Root)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workflow.root %q must remain below the configuration repository", cfg.Workflow.Root)
	}
	workflowRoot := filepath.Join(root, rel)
	if err := ValidateWorkflowPath(root, workflowRoot); err != nil {
		return "", err
	}
	return workflowRoot, nil
}

// ValidateWorkflowPath rejects an existing path component that redirects a
// workflow write outside the configuration repository. Symlinks that resolve
// within the repository remain valid.
func ValidateWorkflowPath(root, path string) error {
	configRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolving configuration repository: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(configRoot, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving workflow path: %w", err)
	}
	rel, err := filepath.Rel(configRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workflow path %s is outside the configuration repository", path)
	}
	resolvedRoot, err := filepath.EvalSymlinks(configRoot)
	if err != nil {
		return fmt.Errorf("resolving configuration repository: %w", err)
	}
	current := configRoot
	for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspecting workflow path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			return fmt.Errorf("resolving workflow symlink %s: %w", current, err)
		}
		inside, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return fmt.Errorf("workflow.root resolves outside the configuration repository through symlink %s", current)
		}
	}
	return nil
}

// WorkflowRootOrDefault preserves read-only diagnostics on an absent or broken
// config while mutating commands reject that configuration through Gate.
func WorkflowRootOrDefault(root string) string {
	resolved, err := WorkflowRoot(root)
	if err != nil {
		return filepath.Join(root, "docs")
	}
	return resolved
}

// MarkWorkflowState records the root that owns durable workflow artifacts.
// A later config edit uses it to refuse an implicit migration.
func MarkWorkflowState(root string) error {
	workflowRoot, err := WorkflowRoot(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, workflowRoot)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, ".homonto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "workflow-root"), []byte(filepath.ToSlash(rel)+"\n"), 0o644)
}

// ValidateWorkflowRootChange gives workflow commands the same fail-closed
// migration guard as config.Load. It is kept here because the CLIs deliberately
// parse only their small gate surface rather than the projection configuration.
func ValidateWorkflowRootChange(root string) error {
	workflowRoot, err := WorkflowRoot(root)
	if err != nil {
		return err
	}
	want, err := filepath.Rel(root, workflowRoot)
	if err != nil {
		return err
	}
	want = filepath.ToSlash(want)
	marker := filepath.Join(root, ".homonto", "workflow-root")
	if data, err := os.ReadFile(marker); err == nil {
		was := filepath.ToSlash(strings.TrimSpace(string(data)))
		if was != "" && was != want && workflowStateExists(filepath.Join(root, filepath.FromSlash(was))) {
			return fmt.Errorf("workflow.root changed from %q to %q while workflow state exists; move or remove the state explicitly before changing the root", was, want)
		}
	}
	if want != "docs" && workflowStateExists(filepath.Join(root, "docs")) {
		return fmt.Errorf("workflow.root changed from %q to %q while workflow state exists; move or remove the state explicitly before changing the root", "docs", want)
	}
	return nil
}

func workflowStateExists(root string) bool {
	for _, name := range []string{"changes", "tasks", ".to-promote"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	return false
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
	return f.GateAny(root)
}

// GateAny is Gate, satisfied when ANY of the given frameworks is declared and
// applied. `to promote` uses it: promotion bridges the two frameworks, so it
// must run both while [frameworks.to] is still declared (the documented
// order) and after the declaration already moved to [frameworks.onto]
// (promoting a leftover `to` change). When none is satisfied, the first
// framework's error is reported — the most specific failure.
func (f Framework) GateAny(root string, others ...Framework) error {
	frameworks := make([]Framework, 0, 1+len(others))
	frameworks = append(frameworks, f)
	frameworks = append(frameworks, others...)
	var firstErr error
	for _, fw := range frameworks {
		if err := fw.gate(root); err == nil {
			return nil
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (f Framework) gate(root string) error {
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
	if _, err := WorkflowRoot(root); err != nil {
		return fmt.Errorf("%s: invalid workflow.root: %w", f.GatePrefix, err)
	}
	if err := ValidateWorkflowRootChange(root); err != nil {
		return fmt.Errorf("%s: %w", f.GatePrefix, err)
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
