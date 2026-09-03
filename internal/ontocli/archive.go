package ontocli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

var datedArchiveName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-(.+)$`)

var errArchiveNotFound = errors.New("no dated archive found")

func locateArchive(root, name string) (string, ontostate.State, error) {
	archiveRoot := filepath.Join(root, "docs", "changes", "archive")
	entries, err := os.ReadDir(archiveRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ontostate.State{}, fmt.Errorf("%w for %q", errArchiveNotFound, name)
		}
		return "", ontostate.State{}, err
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		archiveDir := filepath.Join(archiveRoot, entry.Name())
		m := datedArchiveName.FindStringSubmatch(entry.Name())
		if len(m) == 2 && m[1] == name {
			matches = append(matches, archiveDir)
			continue
		}
		// A same-day numeric suffix is unambiguous only through the state it
		// contains; do not parse suffixes out of valid change names.
		if st, loadErr := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml")); loadErr == nil && st.Change == name {
			matches = append(matches, archiveDir)
		}
	}
	if len(matches) == 0 {
		return "", ontostate.State{}, fmt.Errorf("%w for %q", errArchiveNotFound, name)
	}
	// Newest generation first: date lexically, same-day numeric suffix
	// numerically (generation 10 outranks 9).
	sort.Slice(matches, func(i, j int) bool {
		return ontostate.NewerArchiveName(filepath.Base(matches[i]), filepath.Base(matches[j]))
	})
	selected := matches[0]
	st, err := ontostate.Load(filepath.Join(selected, "onto-state.yaml"))
	if err != nil {
		return "", ontostate.State{}, err
	}
	if err := st.Validate(); err != nil {
		return "", ontostate.State{}, err
	}
	if st.Change != name {
		return "", ontostate.State{}, fmt.Errorf("archived state change %q does not match %q", st.Change, name)
	}
	return selected, st, nil
}

// archiveDestination picks the dated archive target for a close, appending a
// numeric suffix for same-day reuse. Any Lstat outcome other than "free" and
// "taken" (e.g. an unreadable or non-directory parent) is an error rather than
// an infinite suffix walk.
func archiveDestination(root, name, date string) (string, error) {
	base := filepath.Join(root, "docs", "changes", "archive", date+"-"+name)
	for destination, n := base, 2; ; destination, n = fmt.Sprintf("%s-%d", base, n), n+1 {
		_, err := os.Lstat(destination)
		if errors.Is(err, os.ErrNotExist) {
			return destination, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func currentBranch(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("cannot determine source branch (detached HEAD is not integratable)")
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "", fmt.Errorf("cannot determine source branch (detached HEAD is not integratable)")
	}
	return branch, nil
}

func validateBranchName(root, branch string) error {
	if strings.HasPrefix(branch, "-") || branch == "HEAD" || strings.Contains(branch, "@{") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, "git", "-C", root, "check-ref-format", "refs/heads/"+branch).Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	return nil
}

func validateIntegrationRecord(st ontostate.State, record integrationrecord.Record) error {
	if record.Mode != st.Integration {
		return fmt.Errorf("integration record mode %q does not match state integration %q", record.Mode, st.Integration)
	}
	if record.BaseBranch != st.BaseBranch {
		return fmt.Errorf("integration record base branch %q does not match state base_branch %q", record.BaseBranch, st.BaseBranch)
	}
	want := map[string]bool{"": true}
	for _, name := range st.Repos {
		want[name] = true
	}
	got := map[string]bool{}
	for _, entry := range record.Repositories {
		if !want[entry.Alias] {
			return fmt.Errorf("integration record entry %q is outside the change's repository scope", entry.Alias)
		}
		if got[entry.Alias] {
			return fmt.Errorf("integration record has duplicate entry %q", entry.Alias)
		}
		got[entry.Alias] = true
	}
	for name := range want {
		if !got[name] {
			return fmt.Errorf("integration record is missing repository %q", name)
		}
	}
	return nil
}

// captureIntegrationEntries freezes the per-repository integration anchors at
// close time: the config repository plus every declared alias, each recording
// the base branch tip, the current source branch, and its commit. Fail-closed:
// a repository without the base branch, a detached HEAD, or a source equal to
// the base branch refuses the close rather than recording an unintegratable
// state.
func captureIntegrationEntries(root string, st ontostate.State) ([]integrationrecord.Entry, error) {
	type scopedRepo struct{ alias, dir string }
	scopes := []scopedRepo{{"", root}}
	names, dirs, err := scopeDirs(root, st.Repos)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		scopes = append(scopes, scopedRepo{name, dirs[name]})
	}
	entries := make([]integrationrecord.Entry, 0, len(scopes))
	for _, s := range scopes {
		display := s.alias
		if display == "" {
			display = "config"
		}
		baseCommit, err := resolveCommit(s.dir, "refs/heads/"+st.BaseBranch)
		if err != nil {
			return nil, fmt.Errorf("repository %s: base branch %q: %w", display, st.BaseBranch, err)
		}
		sourceBranch, err := currentBranch(s.dir)
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", display, err)
		}
		if sourceBranch == st.BaseBranch {
			return nil, fmt.Errorf("repository %s: source branch is the base branch %q; integrate from a change branch", display, st.BaseBranch)
		}
		sourceCommit, err := resolveCommit(s.dir, "HEAD")
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", display, err)
		}
		entries = append(entries, integrationrecord.Entry{
			Alias: s.alias, BaseBranch: st.BaseBranch, BaseCommit: baseCommit,
			SourceBranch: sourceBranch, SourceCommit: sourceCommit,
		})
	}
	return entries, nil
}

func ignorePendingIntegrationDirt(repos []scopedDirt, name string) []scopedDirt {
	want := filepath.ToSlash(filepath.Join("docs", "changes", name, ".onto", "integration.json"))
	for i := range repos {
		if repos[i].Name != "config" {
			continue
		}
		filtered := repos[i].Entries[:0]
		for _, entry := range repos[i].Entries {
			path := filepath.ToSlash(entry.Path)
			if path == want || strings.HasSuffix(path, "/"+want) {
				continue
			}
			filtered = append(filtered, entry)
		}
		repos[i].Entries = filtered
	}
	return repos
}

func ignoreInterruptedArchiveMoveDirt(repos []scopedDirt) []scopedDirt {
	for i := range repos {
		if repos[i].Name != "config" {
			continue
		}
		filtered := repos[i].Entries[:0]
		for _, entry := range repos[i].Entries {
			if entry.Class == "own" && entry.Status == " D" {
				continue
			}
			filtered = append(filtered, entry)
		}
		repos[i].Entries = filtered
	}
	return repos
}
