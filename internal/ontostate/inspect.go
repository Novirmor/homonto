package ontostate

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RawInspection is a non-mutating view of a versioned onto state document. It
// retains the document's schema version rather than applying the normal
// read-time migration, and names fields a future transformer must preserve or
// reject explicitly.
type RawInspection struct {
	SchemaVersion int
	ID            string
	Change        string
	Workflow      string
	Phase         string
	BaseRef       string
	BaseBranch    string
	Archived      bool
	Abandoned     bool
	Repos         []string
	RepoMode      string
	RepoBases     map[string]RepoBase
	UnknownFields []string
}

// InspectRaw parses a supported, explicitly versioned state without rewriting
// or upgrading it. It is intentionally stricter than Load: migration planning
// must not mistake an unversioned legacy shape for a known preservation rule.
func InspectRaw(b []byte, sourceName string) (RawInspection, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return RawInspection{}, fmt.Errorf("onto-state: %s: expected one YAML document", sourceName)
		}
		return RawInspection{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return RawInspection{}, fmt.Errorf("onto-state: %s: expected exactly one YAML document", sourceName)
		}
		return RawInspection{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return RawInspection{}, fmt.Errorf("onto-state: %s: expected a YAML mapping", sourceName)
	}
	root := document.Content[0]
	if err := validateYAMLMappingKeys(root, ""); err != nil {
		return RawInspection{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	versionNode, ok := yamlMappingValue(root, "schema_version")
	if !ok {
		return RawInspection{}, fmt.Errorf("onto-state: %s: migration planning requires schema_version", sourceName)
	}
	var version int
	if err := versionNode.Decode(&version); err != nil {
		return RawInspection{}, fmt.Errorf("onto-state: %s: invalid schema_version: %w", sourceName, err)
	}
	if version < 1 || version > CurrentSchemaVersion {
		return RawInspection{}, fmt.Errorf("onto-state: %s: unsupported schema_version %d", sourceName, version)
	}
	var state State
	if err := root.Decode(&state); err != nil {
		return RawInspection{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	if state.SchemaVersion != version {
		return RawInspection{}, fmt.Errorf("onto-state: %s: schema_version changed while decoding", sourceName)
	}
	if err := state.Validate(); err != nil {
		return RawInspection{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	unknown, err := unknownStateFields(root)
	if err != nil {
		return RawInspection{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	return RawInspection{
		SchemaVersion: version,
		ID:            state.ID,
		Change:        state.Change,
		Workflow:      state.Workflow,
		Phase:         state.Phase,
		BaseRef:       state.BaseRef,
		BaseBranch:    state.BaseBranch,
		Archived:      state.Archived,
		Abandoned:     state.Abandoned,
		Repos:         append([]string(nil), state.Repos...),
		RepoMode:      state.RepoMode,
		RepoBases:     cloneRepoBases(state.RepoBases),
		UnknownFields: unknown,
	}, nil
}

func cloneRepoBases(in map[string]RepoBase) map[string]RepoBase {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]RepoBase, len(in))
	for alias, base := range in {
		out[alias] = base
	}
	return out
}

func validateYAMLMappingKeys(node *yaml.Node, path string) error {
	switch node.Kind {
	case yaml.AliasNode:
		return fmt.Errorf("aliases are not supported at %s", yamlFieldPath(path))
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := validateYAMLMappingKeys(child, path); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := validateYAMLMappingKeys(child, path); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]bool, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			if i+1 >= len(node.Content) || node.Content[i].Kind != yaml.ScalarNode {
				return fmt.Errorf("mapping key is not a scalar at %s", yamlFieldPath(path))
			}
			key := node.Content[i].Value
			field := yamlJoinPath(path, key)
			if seen[key] {
				return fmt.Errorf("duplicate field %q", field)
			}
			seen[key] = true
			if err := validateYAMLMappingKeys(node.Content[i+1], field); err != nil {
				return err
			}
		}
	}
	return nil
}

func yamlMappingValue(node *yaml.Node, want string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == want {
			return node.Content[i+1], true
		}
	}
	return nil, false
}

func unknownStateFields(root *yaml.Node) ([]string, error) {
	var unknown []string
	if err := collectUnknownFields(root, "", stateRootFields, &unknown); err != nil {
		return nil, err
	}
	sort.Strings(unknown)
	return unknown, nil
}

var stateRootFields = map[string]fieldScanner{
	"schema_version":       {},
	"change":               {},
	"id":                   {},
	"workflow":             {},
	"phase":                {},
	"created":              {},
	"base_ref":             {},
	"base_branch":          {},
	"deps":                 {},
	"repos":                {},
	"repo_mode":            {},
	"repo_bases":           {children: repoBaseFields, dynamic: true},
	"supersedes":           {},
	"deviates_from":        {},
	"isolation":            {},
	"integration":          {},
	"integration_required": {},
	"build_mode":           {},
	"build_pause":          {},
	"tdd_mode":             {},
	"verify":               {children: verifyFields},
	"close":                {children: closeFields},
	"directive":            {},
	"proposal_approved":    {},
	"approach_confirmed":   {},
	"close_confirmed":      {},
	"guides":               {},
	"archived":             {},
	"abandoned":            {},
	"observed":             {children: observedFields},
	// Legacy versioned states can retain these groups. They are reported as
	// known metadata rather than silently treated as transformable current data.
	"decisions": {children: decisionsFields},
	"metrics":   {children: legacyMetricsFields},
}

var (
	repoBaseFields = map[string]fieldScanner{
		"base_ref":       {},
		"base_branch":    {},
		"git_common_dir": {},
	}
	verifyFields = map[string]fieldScanner{
		"scale":  {},
		"mode":   {},
		"result": {},
		"heads":  {dynamic: true},
	}
	closeFields = map[string]fieldScanner{
		"merged": {},
	}
	observedFields = map[string]fieldScanner{
		"metrics":          {dynamic: true},
		"tasks_total":      {},
		"verify_rounds":    {},
		"preset_escalated": {},
	}
	decisionsFields = map[string]fieldScanner{
		"isolation": {},
		"execution": {},
		"tdd":       {},
		"directive": {},
	}
	legacyMetricsFields = map[string]fieldScanner{
		"phases":        {dynamic: true},
		"tasks_total":   {},
		"verify_rounds": {},
		"upgraded":      {},
	}
)

type fieldScanner struct {
	children map[string]fieldScanner
	dynamic  bool
}

func collectUnknownFields(node *yaml.Node, path string, allowed map[string]fieldScanner, unknown *[]string) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		value := node.Content[i+1]
		field := yamlJoinPath(path, key)
		rule, ok := allowed[key]
		if !ok {
			*unknown = append(*unknown, field)
			continue
		}
		if rule.dynamic {
			if rule.children == nil || value.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(value.Content); j += 2 {
				if err := collectUnknownFields(value.Content[j+1], yamlJoinPath(field, value.Content[j].Value), rule.children, unknown); err != nil {
					return err
				}
			}
			continue
		}
		if rule.children != nil {
			if err := collectUnknownFields(value, field, rule.children, unknown); err != nil {
				return err
			}
		}
	}
	return nil
}

func yamlJoinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func yamlFieldPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "document root"
	}
	return path
}
