package ontocli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// gitCmdTimeout bounds each git invocation in the worktree-dirt scan. A git
// command can block indefinitely on a credential prompt, a corrupt repo, or a
// hung FS (NFS/FUSE); without a deadline the close/advance gates would hang
// the whole CLI. remote/fetch.go uses the same idiom for its git fetches.
const gitCmdTimeout = 30 * time.Second

// dirtEntry is one uncommitted path, classified structurally so gates and
// agents can tell "this change's own evidence" from "another change's
// business" from "source code" without judgment calls (B1: shape, not intent).
type dirtEntry struct {
	Path   string `json:"path"`   // repo-root-relative, as git reports it
	Status string `json:"status"` // two-letter porcelain XY code ("??", " M", ...)
	// Class is "own" (under this change's docs/changes/<name>/, or an
	// ancestor directory that contains it), "change" (under docs/changes/
	// but belonging to a different change or the archive), or "source"
	// (anything else in the repo).
	Class string `json:"class"`
	// BlocksClose: a change may not close while its own artifacts or any
	// source path is uncommitted; another change's docs are that change's
	// obligation and do not block this one.
	BlocksClose bool `json:"blocks_close"`
}

// worktreeDirt lists the uncommitted paths of the git worktree containing
// root, each classified relative to change (pass "" when no change is in
// scope — nothing is then "own"). determinable is false when git is
// unavailable or root is not inside a repository; callers must treat that as
// "unknown," never as clean. The scan is repo-wide even when root is a
// subdirectory, matching the close gate's promise that the whole tree is
// committed.
func worktreeDirt(root, change string) (entries []dirtEntry, determinable bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "-z").Output()
	if err != nil {
		return nil, false
	}
	prefixOut, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-prefix").Output()
	if err != nil {
		return nil, false
	}
	prefix := strings.TrimSpace(string(prefixOut)) // "" at repo root, "sub/dir/" below it

	docsPrefix := prefix + "docs/changes/"
	ownPrefix := ""
	if change != "" {
		ownPrefix = docsPrefix + change + "/"
	}

	// -z output: NUL-separated "XY path" records; a rename/copy record is
	// followed by one extra NUL-terminated token holding the original path.
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if len(tok) < 4 || tok[2] != ' ' {
			continue
		}
		status, p := tok[:2], tok[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++ // skip the rename/copy source token
		}
		e := dirtEntry{Path: p, Status: status, Class: classifyDirt(p, docsPrefix, ownPrefix)}
		e.BlocksClose = e.Class != "change"
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, true
}

// classifyDirt maps one repo-relative path to its dirt class. An ancestor
// directory reported dirty (git shows an entirely-untracked directory as a
// single "dir/" entry) is classified "own" when it contains the change's
// directory — conservative: it may hold the change's uncommitted evidence.
func classifyDirt(p, docsPrefix, ownPrefix string) string {
	if ownPrefix != "" {
		if strings.HasPrefix(p, ownPrefix) || p == strings.TrimSuffix(ownPrefix, "/") {
			return "own"
		}
		if strings.HasPrefix(ownPrefix, ensureSlash(p)) {
			return "own"
		}
	}
	if strings.HasPrefix(p, docsPrefix) {
		return "change"
	}
	return "source"
}

func ensureSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

// worktreeDirty is the coarse yes/no view of worktreeDirt, kept for callers
// (and gates) that only care whether ANYTHING is uncommitted.
func worktreeDirty(root string) (dirty bool, determinable bool) {
	entries, ok := worktreeDirt(root, "")
	return len(entries) > 0, ok
}

// blockingDirt filters to the entries that block closing the change the
// classification was computed against.
func blockingDirt(entries []dirtEntry) []dirtEntry {
	var out []dirtEntry
	for _, e := range entries {
		if e.BlocksClose {
			out = append(out, e)
		}
	}
	return out
}

// dirtGateError renders a close-blocking dirt list as an actionable error
// body: the first few offending paths verbatim, the count of the rest, how
// much foreign-change dirt was tolerated, and the command that shows it all.
func dirtGateError(blocking []dirtEntry, total int, change string) string {
	const show = 5
	var b strings.Builder
	fmt.Fprintf(&b, "%d uncommitted path(s) must be committed or stashed first:", len(blocking))
	for i, e := range blocking {
		if i == show {
			fmt.Fprintf(&b, "\n  … and %d more", len(blocking)-show)
			break
		}
		fmt.Fprintf(&b, "\n  %s %s", e.Status, e.Path)
	}
	if ignored := total - len(blocking); ignored > 0 {
		fmt.Fprintf(&b, "\n(%d path(s) under other changes' docs/changes/ do not block and were not counted)", ignored)
	}
	fmt.Fprintf(&b, "\nrun `onto dirt %s` for the full classified list", change)
	return b.String()
}

// dirtCmd builds "onto dirt [change] [--json]": a read-only, classified report
// of every uncommitted path — the deterministic half of the dirty-workspace
// protocol. Skills attribute and decide; the binary owns what-is-dirty and
// what-blocks-close (like `onto gate`, the schema lives here, not in prose).
func dirtCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "dirt [change]",
		Short: "List uncommitted paths, classified against a change (read-only)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			change := ""
			if len(args) == 1 {
				change = args[0]
				if err := ontoFramework.ValidChangeName(change); err != nil {
					return err
				}
			}
			// A recorded cross-repo scope extends the diagnostic to exactly the
			// repositories that close will gate. Keep the historic root-only
			// behavior for ad-hoc names and legacy states without repos.
			if change != "" {
				if st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", change, "onto-state.yaml")); err == nil && len(st.Repos) > 0 {
					repos, err := scopedWorktreeDirt(dir, change, st.Repos)
					if err != nil {
						return fmt.Errorf("onto dirt: cannot determine scoped worktree state: %w", err)
					}
					return renderScopedDirt(cmd, change, repos, asJSON)
				}
			}
			entries, determinable := worktreeDirt(dir, change)
			if !determinable {
				return fmt.Errorf("onto dirt: cannot determine worktree state (is %s inside a git repository?)", path.Clean(dir))
			}
			blocking := blockingDirt(entries)
			if asJSON {
				report := struct {
					Change        string      `json:"change,omitempty"`
					Clean         bool        `json:"clean"`
					BlockingClose int         `json:"blocking_close"`
					Entries       []dirtEntry `json:"entries"`
				}{change, len(entries) == 0, len(blocking), entries}
				if report.Entries == nil {
					report.Entries = []dirtEntry{}
				}
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "clean: no uncommitted paths")
				return nil
			}
			for _, e := range entries {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", e.Status, e.Path, e.Class)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d uncommitted path(s), %d blocking close\n", len(entries), len(blocking))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to inspect")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the classified dirt report as JSON")
	return cmd
}

func renderScopedDirt(cmd *cobra.Command, change string, repos []scopedDirt, asJSON bool) error {
	total, blocking := 0, 0
	for _, repo := range repos {
		total += len(repo.Entries)
		blocking += len(blockingDirt(repo.Entries))
	}
	if asJSON {
		report := struct {
			Change        string       `json:"change"`
			Clean         bool         `json:"clean"`
			BlockingClose int          `json:"blocking_close"`
			Repositories  []scopedDirt `json:"repositories"`
		}{change, total == 0, blocking, repos}
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	}
	if total == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "clean: no uncommitted paths")
		return nil
	}
	for _, repo := range repos {
		for _, e := range repo.Entries {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s %s (%s)\n", repo.Name, e.Status, e.Path, e.Class)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%d uncommitted path(s), %d blocking close\n", total, blocking)
	return nil
}
