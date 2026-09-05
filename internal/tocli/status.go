package tocli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// statusEntry is one active change as "to status" reports it. Error is set
// when a change directory exists but its state is missing or malformed —
// status reports it rather than failing the whole listing.
type statusEntry struct {
	Change   string   `json:"change"`
	Phase    string   `json:"phase,omitempty"`
	Created  string   `json:"created,omitempty"`
	Verified bool     `json:"verified,omitempty"`
	Repos    []string `json:"repos,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// statusCmd builds "to status": read-only and config-independent — it never
// reads homonto.toml and never writes. It lists every active (non-archived)
// change and its phase. With --all it also lists the onto workflow's active
// changes (global inventory, ADR 0042) — JSON then emits an object with
// "tasks" and "onto" arrays instead of the bare task array.
func statusCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
		all      bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "List active changes and their phases (read-only; --all adds the onto workflow's)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, err := collectStatus(dir)
			if err != nil {
				return err
			}
			if all {
				siblings, err := collectSiblingStatus(dir)
				if err != nil {
					return err
				}
				if jsonMode {
					return printJSON(cmd, map[string]any{"tasks": entries, "onto": siblings})
				}
				for _, e := range entries {
					cmd.Printf("%s\t%s\n", e.Change, phaseOf(e))
				}
				if len(siblings) > 0 {
					cmd.Println("onto:")
					for _, s := range siblings {
						cmd.Printf("%s\t%s\n", s.Change, s.Phase)
					}
				}
				return nil
			}
			if jsonMode {
				return printJSON(cmd, entries)
			}
			if len(entries) == 0 {
				cmd.Println("no active changes")
				return nil
			}
			for _, e := range entries {
				if e.Error != "" {
					cmd.Printf("%s\tinvalid\t%s\n", e.Change, e.Error)
					continue
				}
				cmd.Printf("%s\t%s\n", e.Change, phaseOf(e))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to inspect")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "also list the onto workflow's active changes (combined inventory)")
	return cmd
}

// phaseOf renders one entry's status column (phase, with repo scope).
func phaseOf(e statusEntry) string {
	if len(e.Repos) == 0 {
		return e.Phase
	}
	return fmt.Sprintf("%s\trepos=%v", e.Phase, e.Repos)
}

// collectSiblingStatus lists the onto workflow's active changes (name and
// recorded phase) for the combined --all inventory. Read-only; a missing
// changes/ tree is an empty listing.
func collectSiblingStatus(root string) ([]statusEntry, error) {
	wf, err := workcli.WorkflowRoot(root)
	if err != nil {
		return []statusEntry{}, nil
	}
	entries, err := os.ReadDir(filepath.Join(wf, "changes"))
	if os.IsNotExist(err) {
		return []statusEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("to status: reading sibling changes: %w", err)
	}
	out := []statusEntry{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" {
			continue
		}
		st, err := ontostate.LoadChange(filepath.Join(wf, "changes", e.Name()))
		if err == nil {
			err = st.Validate()
		}
		if err != nil {
			out = append(out, statusEntry{Change: e.Name(), Error: err.Error()})
			continue
		}
		out = append(out, statusEntry{Change: e.Name(), Phase: st.Phase, Created: st.Created, Repos: st.Repos})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Change < out[j].Change })
	return out, nil
}

// collectStatus scans docs/tasks/ for change directories, skipping the
// archive. A missing docs/tasks/ is an empty listing, not an error, so
// status works in any repo.
func collectStatus(root string) ([]statusEntry, error) {
	dirents, err := os.ReadDir(tasksDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return []statusEntry{}, nil
		}
		return nil, fmt.Errorf("to status: reading %s: %w", tasksDir(root), err)
	}

	entries := []statusEntry{}
	for _, d := range dirents {
		if !d.IsDir() || d.Name() == "archive" {
			continue
		}
		name := d.Name()
		st, err := tostate.Load(statePath(root, name))
		if err == nil {
			err = st.Validate()
		}
		if err != nil {
			entries = append(entries, statusEntry{Change: name, Error: err.Error()})
			continue
		}
		entries = append(entries, statusEntry{
			Change:   name,
			Phase:    st.Phase,
			Created:  st.Created,
			Verified: st.Verified,
			Repos:    st.Repos,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Change < entries[j].Change })
	return entries, nil
}
