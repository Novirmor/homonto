package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/baseadapter"
	"github.com/noviopenworks/homonto/internal/adapter/jsoncodec"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
func trim(s, p string) string    { return strings.TrimPrefix(s, p) }

// managedPrefix reports whether a state key is pruned by the generic delete
// loop: the file-projection prefixes. The structured-document prefixes
// (mcp./projmcp.) are pruned by their structproj.Project calls instead, so
// they are excluded here to avoid a double delete; subagentcopy.* is pruned by
// its own reconciler pass.
func managedPrefix(k string) bool {
	return baseadapter.HasAnyPrefix(k, "skill.", "command.", "subagent.")
}

// staleStructuredPrefixes are the structured namespaces pre-v0.13.0 claude
// adapters recorded that this adapter does not manage: settings, project
// settings, plugins, plugin configs, and marketplaces were removed with the
// claude adapter in v0.13.0 and are not restored by the claude target yet
// (ADR 0066 keeps them fail-closed at load). Records under these prefixes are
// retired state-only once the adapter is activated.
var staleStructuredPrefixes = []string{"setting.", "projsetting.", "plugin.", "pluginconfig.", "marketplace."}

func staleStructuredKey(k string) bool {
	return baseadapter.HasAnyPrefix(k, staleStructuredPrefixes...)
}

// filterChanges returns the subset of changes whose keys are in prefix, so each
// structproj namespace applies only the changes it owns.
func filterChanges(changes []adapter.Change, prefix string) []adapter.Change {
	return baseadapter.FilterChanges(changes, prefix)
}

// readStandardized reads a managed tool document normalized for the codec
// (missing file = empty object; unparseable = error; non-object root named).
func readStandardized(path string) ([]byte, error) {
	return baseadapter.ReadStandardizedJSON(path)
}

// FileForKey names the document a recorded structured key is managed in, for
// the engine's cross-partition destination-conflict detection (see
// adapter.KeyFiler). Only the MCP namespaces have a fixed destination in this
// slice; every other key returns "" (links carry their own dst in state).
func (a *Adapter) FileForKey(key string) string {
	switch {
	case hasPrefix(key, "mcp."):
		return a.claudeJSON()
	case hasPrefix(key, "projmcp."):
		if a.ProjectRoot == "" {
			return ""
		}
		return a.projectMCPJSON()
	}
	return ""
}

// checkRegistryRelocation fails the plan when an absolute $CLAUDE_CONFIG_DIR
// has moved homonto's MCP registry while previously managed servers still
// live in the default $HOME/.claude.json — applying under the override would
// strand them (and any resolved credential env) in the old file with nothing
// left to prune them. An entry counts as stranded only when its current
// on-disk value in the old registry hashes to the recorded Entry.Applied, so
// foreign or user-rewritten entries never block the plan.
func (a *Adapter) checkRegistryRelocation(st *state.State) error {
	override := os.Getenv("CLAUDE_CONFIG_DIR")
	if !filepath.IsAbs(override) {
		return nil // no relocation in effect
	}
	oldPath := filepath.Join(a.Home, ".claude.json")
	if oldPath == a.claudeJSON() {
		return nil // the override resolves to the same file: nothing moved
	}
	doc, err := readStandardized(oldPath)
	if err != nil {
		return nil // old registry unparseable: cannot prove stranding
	}
	var stranded []string
	for _, key := range st.Keys("claude") {
		if !hasPrefix(key, "mcp.") {
			continue
		}
		entry, ok := st.Get("claude", key)
		if !ok || entry.Applied == "" {
			continue
		}
		val, present, err := jsoncodec.Codec{}.Get(doc, mcpDocPath(key))
		if err != nil || !present {
			continue
		}
		if secret.Hash(val) == entry.Applied {
			stranded = append(stranded, trim(key, "mcp."))
		}
	}
	if len(stranded) == 0 {
		return nil
	}
	sort.Strings(stranded)
	return fmt.Errorf("CLAUDE_CONFIG_DIR moved homonto's MCP registry to %s while %d managed server(s) from this config still live in %s (%s); remove them there or unset CLAUDE_CONFIG_DIR before applying — refusing to strand them",
		filepath.Join(override, ".claude.json"), len(stranded), filepath.Join(a.Home, ".claude.json"), strings.Join(stranded, ", "))
}
