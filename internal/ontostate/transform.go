package ontostate

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidatedRepoAnchor is one already-validated source provenance anchor. The
// transformer still checks its shape and exact scope so callers cannot widen a
// record or replace existing provenance by accident.
type ValidatedRepoAnchor struct {
	Alias        string
	BaseRef      string
	BaseBranch   string
	GitCommonDir string
}

const LegacyConfigProvenance = "unverified_attested_control_only"

// LegacyConfig retains scalar base fields removed from an active postimage.
// It is receipt metadata only: it does not identify or authorize a selected
// source repository.
type LegacyConfig struct {
	BaseRef    string `json:"base_ref"`
	BaseBranch string `json:"base_branch"`
	Provenance string `json:"provenance"`
}

// TransformResult is the pure postimage for one active state. Bytes are kept
// out of migration-plan JSON; a caller that intends to write them must arrange
// its own locked journal and atomic replacement. LegacyConfig must be retained
// with the exact preimage before a future apply can accept the postimage.
type TransformResult struct {
	Bytes         []byte
	ChangedFields []string
	LegacyConfig  *LegacyConfig
}

// TransformActiveForExplicitRepos upgrades one supported, versioned, flat
// active state to explicit repository provenance. It never reads or writes the
// filesystem. The provided anchors must exactly cover the state-selected
// aliases. Scalar base_ref and base_branch are removed from the operative state
// and returned only as unverified, control-only receipt metadata.
func TransformActiveForExplicitRepos(raw []byte, expectedID string, anchors []ValidatedRepoAnchor) (TransformResult, error) {
	inspection, err := InspectRaw(raw, "migration state")
	if err != nil {
		return TransformResult{}, fmt.Errorf("onto-state migration: %w", err)
	}
	if strings.TrimSpace(expectedID) == "" {
		return TransformResult{}, fmt.Errorf("onto-state migration: expected ID is required")
	}
	if inspection.ID != expectedID {
		return TransformResult{}, fmt.Errorf("onto-state migration: expected ID does not match state")
	}
	if inspection.Archived || inspection.Abandoned {
		return TransformResult{}, fmt.Errorf("onto-state migration: only active states can be transformed")
	}

	document, root, err := migrationYAMLDocument(raw)
	if err != nil {
		return TransformResult{}, err
	}
	if err := rejectLegacyNestedMigrationFields(root); err != nil {
		return TransformResult{}, err
	}
	if inspection.SchemaVersion < CurrentSchemaVersion && (hasYAMLMappingValue(root, "repo_mode") || hasYAMLMappingValue(root, "repo_bases")) {
		return TransformResult{}, fmt.Errorf("onto-state migration: legacy schema state already carries source provenance")
	}
	var before State
	if err := root.Decode(&before); err != nil {
		return TransformResult{}, fmt.Errorf("onto-state migration: decode supported state: %w", err)
	}
	if inspection.SchemaVersion == 1 && before.IntegrationRequired {
		return TransformResult{}, fmt.Errorf("onto-state migration: schema-1 integration_required cannot be transformed without changing normal loader semantics")
	}
	effectiveBefore, err := effectiveMigrationState(raw, before)
	if err != nil {
		return TransformResult{}, err
	}

	approvedBases, err := approvedMigrationBases(inspection.Repos, anchors)
	if err != nil {
		return TransformResult{}, err
	}
	if inspection.RepoMode != "" && inspection.RepoMode != "explicit" {
		return TransformResult{}, fmt.Errorf("onto-state migration: existing legacy source provenance cannot be replaced")
	}
	if len(inspection.RepoBases) != 0 && !sameRepoBases(inspection.RepoBases, approvedBases) {
		return TransformResult{}, fmt.Errorf("onto-state migration: existing source provenance does not exactly match approved anchors")
	}

	legacyConfig, hasBaseRef, hasBaseBranch := legacyConfigFromRaw(root, inspection)
	changed := migrationChangedFields(inspection, approvedBases, hasBaseRef, hasBaseBranch)
	if len(changed) == 0 {
		return TransformResult{Bytes: append([]byte(nil), raw...), ChangedFields: []string{}}, nil
	}

	beforeRoot := cloneYAMLNode(root)

	if inspection.SchemaVersion != CurrentSchemaVersion {
		setYAMLMappingValue(root, "schema_version", yamlIntNode(CurrentSchemaVersion))
	}
	if inspection.RepoMode != "explicit" {
		setYAMLMappingValue(root, "repo_mode", yamlStringNode("explicit"))
	}
	if !sameRepoBases(inspection.RepoBases, approvedBases) {
		setYAMLMappingValue(root, "repo_bases", yamlRepoBasesNode(approvedBases))
	}
	if hasBaseRef {
		deleteYAMLMappingValue(root, "base_ref")
	}
	if hasBaseBranch {
		deleteYAMLMappingValue(root, "base_branch")
	}

	postimage, err := yaml.Marshal(document)
	if err != nil {
		return TransformResult{}, fmt.Errorf("onto-state migration: encode transformed state: %w", err)
	}
	postInspection, err := InspectRaw(postimage, "transformed migration state")
	if err != nil {
		return TransformResult{}, fmt.Errorf("onto-state migration: transformed state is invalid: %w", err)
	}
	if postInspection.ID != expectedID || postInspection.SchemaVersion != CurrentSchemaVersion || postInspection.RepoMode != "explicit" || postInspection.BaseRef != "" || postInspection.BaseBranch != "" || !sameRepoBases(postInspection.RepoBases, approvedBases) {
		return TransformResult{}, fmt.Errorf("onto-state migration: transformed provenance did not match approved anchors")
	}

	var after State
	if after, err = parseAndMigrate(postimage, "transformed migration state"); err != nil {
		return TransformResult{}, fmt.Errorf("onto-state migration: load transformed state: %w", err)
	}
	if err := after.Validate(); err != nil {
		return TransformResult{}, fmt.Errorf("onto-state migration: validate transformed state: %w", err)
	}
	want := effectiveBefore
	want.RepoMode = "explicit"
	want.RepoBases = cloneRepoBases(approvedBases)
	want.BaseRef = ""
	want.BaseBranch = ""
	if !reflect.DeepEqual(want, after) {
		return TransformResult{}, fmt.Errorf("onto-state migration: transformed state changed fields outside explicit provenance")
	}

	_, afterRoot, err := migrationYAMLDocument(postimage)
	if err != nil {
		return TransformResult{}, err
	}
	if !yamlMappingEquivalentExcept(beforeRoot, afterRoot, map[string]bool{
		"schema_version": true,
		"repo_mode":      true,
		"repo_bases":     true,
		"base_ref":       true,
		"base_branch":    true,
	}) {
		return TransformResult{}, fmt.Errorf("onto-state migration: transformed YAML did not preserve non-provenance fields")
	}
	return TransformResult{Bytes: postimage, ChangedFields: changed, LegacyConfig: legacyConfig}, nil
}

// effectiveMigrationState checks the direct YAML decode against the normal
// versioned-state loader. M2 preserves non-provenance YAML fields verbatim, so
// it must refuse any state whose normal read migration would change one.
func effectiveMigrationState(raw []byte, direct State) (State, error) {
	effective, err := parseAndMigrate(raw, "migration state")
	if err != nil {
		return State{}, fmt.Errorf("onto-state migration: normal loader rejected state: %w", err)
	}
	direct.SchemaVersion = CurrentSchemaVersion
	if !reflect.DeepEqual(direct, effective) {
		return State{}, fmt.Errorf("onto-state migration: normal loader changes non-provenance state semantics")
	}
	return effective, nil
}

func migrationYAMLDocument(raw []byte) (*yaml.Node, *yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, nil, fmt.Errorf("onto-state migration: decode YAML: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("onto-state migration: expected a YAML mapping")
	}
	return &document, document.Content[0], nil
}

func rejectLegacyNestedMigrationFields(root *yaml.Node) error {
	for _, field := range []string{"decisions", "metrics"} {
		if hasYAMLMappingValue(root, field) {
			return fmt.Errorf("onto-state migration: legacy nested %s cannot be transformed without losing semantics", field)
		}
	}
	verify, ok := yamlMappingValue(root, "verify")
	if ok && verify.Kind == yaml.MappingNode && hasYAMLMappingValue(verify, "mode") {
		return fmt.Errorf("onto-state migration: legacy verify.mode cannot be transformed without losing semantics")
	}
	return nil
}

func approvedMigrationBases(repos []string, anchors []ValidatedRepoAnchor) (map[string]RepoBase, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("onto-state migration: active explicit provenance requires selected repositories")
	}
	expected := make(map[string]bool, len(repos))
	for _, alias := range repos {
		if !validMigrationAlias(alias) || expected[alias] {
			return nil, fmt.Errorf("onto-state migration: state has invalid selected repository aliases")
		}
		expected[alias] = true
	}
	bases := make(map[string]RepoBase, len(anchors))
	seen := make(map[string]bool, len(anchors))
	for _, anchor := range anchors {
		if !validMigrationAlias(anchor.Alias) || !expected[anchor.Alias] || seen[anchor.Alias] {
			return nil, fmt.Errorf("onto-state migration: approved anchors must exactly cover selected repository aliases")
		}
		if !canonicalCommit.MatchString(anchor.BaseRef) || strings.TrimSpace(anchor.BaseBranch) == "" || !filepath.IsAbs(anchor.GitCommonDir) || filepath.Clean(anchor.GitCommonDir) != anchor.GitCommonDir {
			return nil, fmt.Errorf("onto-state migration: approved anchor is incomplete or invalid")
		}
		seen[anchor.Alias] = true
		bases[anchor.Alias] = RepoBase{BaseRef: anchor.BaseRef, BaseBranch: anchor.BaseBranch, GitCommonDir: anchor.GitCommonDir}
	}
	if len(bases) != len(expected) {
		return nil, fmt.Errorf("onto-state migration: approved anchors must exactly cover selected repository aliases")
	}
	return bases, nil
}

func validMigrationAlias(alias string) bool {
	return alias != "" && alias != "." && alias != ".." && !strings.ContainsAny(alias, "/\\") && strings.IndexFunc(alias, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func legacyConfigFromRaw(root *yaml.Node, inspection RawInspection) (*LegacyConfig, bool, bool) {
	_, hasBaseRef := yamlMappingValue(root, "base_ref")
	_, hasBaseBranch := yamlMappingValue(root, "base_branch")
	if !hasBaseRef && !hasBaseBranch {
		return nil, false, false
	}
	return &LegacyConfig{
		BaseRef:    inspection.BaseRef,
		BaseBranch: inspection.BaseBranch,
		Provenance: LegacyConfigProvenance,
	}, hasBaseRef, hasBaseBranch
}

func migrationChangedFields(inspection RawInspection, bases map[string]RepoBase, hasBaseRef, hasBaseBranch bool) []string {
	changed := make([]string, 0, 5)
	if inspection.SchemaVersion != CurrentSchemaVersion {
		changed = append(changed, "schema_version")
	}
	if inspection.RepoMode != "explicit" {
		changed = append(changed, "repo_mode")
	}
	if !sameRepoBases(inspection.RepoBases, bases) {
		changed = append(changed, "repo_bases")
	}
	if hasBaseRef {
		changed = append(changed, "base_ref")
	}
	if hasBaseBranch {
		changed = append(changed, "base_branch")
	}
	return changed
}

func sameRepoBases(a, b map[string]RepoBase) bool {
	if len(a) != len(b) {
		return false
	}
	for alias, base := range a {
		if b[alias] != base {
			return false
		}
	}
	return true
}

func hasYAMLMappingValue(node *yaml.Node, key string) bool {
	_, ok := yamlMappingValue(node, key)
	return ok
}

func setYAMLMappingValue(root *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1] = value
			return
		}
	}
	root.Content = append(root.Content, yamlStringNode(key), value)
}

func deleteYAMLMappingValue(root *yaml.Node, key string) bool {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return true
		}
	}
	return false
}

func yamlStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlIntNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", value)}
}

func yamlRepoBasesNode(bases map[string]RepoBase) *yaml.Node {
	aliases := make([]string, 0, len(bases))
	for alias := range bases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, alias := range aliases {
		base := bases[alias]
		baseNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		baseNode.Content = append(baseNode.Content,
			yamlStringNode("base_ref"), yamlStringNode(base.BaseRef),
			yamlStringNode("base_branch"), yamlStringNode(base.BaseBranch),
			yamlStringNode("git_common_dir"), yamlStringNode(base.GitCommonDir),
		)
		node.Content = append(node.Content, yamlStringNode(alias), baseNode)
	}
	return node
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneYAMLNode(child)
	}
	return &clone
}

func yamlMappingEquivalentExcept(before, after *yaml.Node, excluded map[string]bool) bool {
	if before == nil || after == nil || before.Kind != yaml.MappingNode || after.Kind != yaml.MappingNode {
		return false
	}
	beforeValues := yamlMappingNodes(before)
	afterValues := yamlMappingNodes(after)
	for key, beforeEntry := range beforeValues {
		if excluded[key] {
			continue
		}
		afterEntry, ok := afterValues[key]
		if !ok || !yamlNodesEquivalent(beforeEntry.key, afterEntry.key) || !yamlNodesEquivalent(beforeEntry.value, afterEntry.value) {
			return false
		}
	}
	for key := range afterValues {
		if !excluded[key] {
			if _, ok := beforeValues[key]; !ok {
				return false
			}
		}
	}
	return true
}

type yamlMappingEntry struct {
	key   *yaml.Node
	value *yaml.Node
}

func yamlMappingNodes(node *yaml.Node) map[string]yamlMappingEntry {
	entries := make(map[string]yamlMappingEntry, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		entries[node.Content[i].Value] = yamlMappingEntry{key: node.Content[i], value: node.Content[i+1]}
	}
	return entries
}

func yamlNodesEquivalent(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value || a.Anchor != b.Anchor || a.HeadComment != b.HeadComment || a.LineComment != b.LineComment || a.FootComment != b.FootComment || len(a.Content) != len(b.Content) {
		return false
	}
	if (a.Alias == nil) != (b.Alias == nil) {
		return false
	}
	for i := range a.Content {
		if !yamlNodesEquivalent(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}
