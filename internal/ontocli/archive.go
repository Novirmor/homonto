package ontocli

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	archiveRoot := ontoArchiveDir(root)
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
	base := filepath.Join(ontoArchiveDir(root), date+"-"+name)
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
	out, err := gitAt(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
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
	if err := gitAt(ctx, root, "check-ref-format", "refs/heads/"+branch).Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	return nil
}

func validateIntegrationRecord(st ontostate.State, record integrationrecord.Record) error {
	if err := st.Validate(); err != nil {
		return err
	}
	if err := record.Validate(st.Change); err != nil {
		return err
	}
	// Legacy provenance pins identities without changing the combined-layout
	// integration format, whose implicit-config mode is the empty string.
	mode := st.RepoMode
	if mode == "legacy" {
		mode = ""
	}
	if record.RepoMode != mode {
		return fmt.Errorf("integration record repository mode does not match state")
	}
	if record.Mode != st.Integration {
		return fmt.Errorf("integration record mode %q does not match state integration %q", record.Mode, st.Integration)
	}
	if record.BaseBranch != st.BaseBranch {
		return fmt.Errorf("integration record base branch %q does not match state base_branch %q", record.BaseBranch, st.BaseBranch)
	}
	want := map[string]bool{"": true}
	if st.RepoMode == "explicit" {
		delete(want, "")
	}
	for _, name := range st.Repos {
		want[name] = true
	}
	got := map[string]bool{}
	for _, entry := range record.Repositories {
		if st.RepoMode == "explicit" && entry.BaseBranch != st.RepoBases[entry.Alias].BaseBranch {
			return fmt.Errorf("integration record repository %s base branch does not match state", entry.Alias)
		}
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
	dirs, err := stateSourceDirs(root, st)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dirs))
	for name := range dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]integrationrecord.Entry, 0, len(dirs))
	for _, alias := range names {
		dir := dirs[alias]
		branch := st.BaseBranch
		if st.RepoMode == "explicit" {
			branch = st.RepoBases[alias].BaseBranch
		}
		display := alias
		if display == "" {
			display = "config"
		}
		baseCommit, err := resolveCommit(dir, "refs/heads/"+branch)
		if err != nil {
			return nil, fmt.Errorf("repository %s: base branch %q: %w", display, branch, err)
		}
		sourceBranch, err := currentBranch(dir)
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", display, err)
		}
		if sourceBranch == branch && st.RepoMode != "explicit" {
			return nil, fmt.Errorf("repository %s: source branch is the base branch %q; integrate from a change branch", display, branch)
		}
		sourceCommit, err := resolveCommit(dir, "HEAD")
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", display, err)
		}
		entry := integrationrecord.Entry{
			Alias: alias, BaseBranch: branch, BaseCommit: baseCommit,
			SourceBranch: sourceBranch, SourceCommit: sourceCommit,
		}
		if err := validateIntegrationSource(root, dir, st, entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func ignorePendingIntegrationDirt(root string, repos []scopedDirt, name string) []scopedDirt {
	for i := range repos {
		workflowRel, owner := recordsGitPrefix(root, repos[i].Dir)
		if !owner {
			continue
		}
		want := filepath.ToSlash(filepath.Join(workflowRel, "changes", name, ".onto", "integration.json"))
		filtered := repos[i].Entries[:0]
		for _, entry := range repos[i].Entries {
			path := filepath.ToSlash(entry.Path)
			if path == want {
				continue
			}
			filtered = append(filtered, entry)
		}
		repos[i].Entries = filtered
	}
	return repos
}

func ignoreInterruptedArchiveMoveDirt(root, name string, repos []scopedDirt) []scopedDirt {
	for i := range repos {
		workflowRel, owner := recordsGitPrefix(root, repos[i].Dir)
		if !owner {
			continue
		}
		prefix := filepath.ToSlash(filepath.Join(workflowRel, "changes", name)) + "/"
		filtered := repos[i].Entries[:0]
		for _, entry := range repos[i].Entries {
			if strings.HasPrefix(entry.Path, prefix) && entry.Status == " D" {
				continue
			}
			filtered = append(filtered, entry)
		}
		repos[i].Entries = filtered
	}
	return repos
}
