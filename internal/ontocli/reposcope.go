package ontocli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/config"
)

// scopedDirt is the live git audit for one repository participating in an
// onto change. The config repository is always present as "config"; declared
// repositories are included only when the change recorded their aliases.
type scopedDirt struct {
	Name    string      `json:"name"`
	Dir     string      `json:"dir"`
	Entries []dirtEntry `json:"entries"`
}

// scopeDirs resolves the stored [repos] aliases at the moment they are used.
// A missing or renamed alias is a close-gate failure, never silently omitted.
func scopeDirs(root string, repos []string) ([]string, map[string]string, error) {
	names := append([]string(nil), repos...)
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			return nil, nil, fmt.Errorf("invalid cross-repo scope")
		}
		seen[name] = true
	}
	if len(names) == 0 {
		return names, nil, nil
	}
	cfg, err := config.Load(filepath.Join(root, "homonto.toml"))
	if err != nil {
		return nil, nil, fmt.Errorf("load declared repos: %w", err)
	}
	dirs := cfg.RepoDirs()
	for _, name := range names {
		if _, ok := dirs[name]; !ok {
			return nil, nil, fmt.Errorf("repo %q is no longer declared under [repos]", name)
		}
	}
	return names, dirs, nil
}

// scopedWorktreeDirt audits the config repo and every selected declared repo.
// The config-repo scan retains onto's change-artifact carve-out; external
// repositories have no central workflow tree, so every uncommitted path is
// blocking there.
func scopedWorktreeDirt(root, change string, repos []string) ([]scopedDirt, error) {
	entries, ok := worktreeDirt(root, change)
	if !ok {
		return nil, fmt.Errorf("cannot determine config repo worktree state")
	}
	out := []scopedDirt{{Name: "config", Dir: root, Entries: entries}}
	names, dirs, err := scopeDirs(root, repos)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		entries, ok := worktreeDirt(dirs[name], "")
		if !ok {
			return nil, fmt.Errorf("cannot determine declared repo %q worktree state", name)
		}
		out = append(out, scopedDirt{Name: name, Dir: dirs[name], Entries: entries})
	}
	return out, nil
}

// scopedDirtGateError renders all close-blocking dirt with repository labels.
func scopedDirtGateError(repos []scopedDirt, change string) string {
	var lines []string
	for _, repo := range repos {
		for _, entry := range blockingDirt(repo.Entries) {
			lines = append(lines, fmt.Sprintf("  %s: %s %s", repo.Name, entry.Status, entry.Path))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("%d uncommitted path(s) must be committed or stashed first:\n%s\nrun `onto dirt %s` for the full classified list", len(lines), joinLines(lines), change)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for _, line := range lines[1:] {
		out += "\n" + line
	}
	return out
}
