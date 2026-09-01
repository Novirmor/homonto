package tocli

import (
	"fmt"
	"os"
	"sort"

	"github.com/noviopenworks/homonto/internal/tostate"
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
// change and its phase.
func statusCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "List active changes and their phases (read-only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, err := collectStatus(dir)
			if err != nil {
				return err
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
				if len(e.Repos) == 0 {
					cmd.Printf("%s\t%s\n", e.Change, e.Phase)
					continue
				}
				cmd.Printf("%s\t%s\trepos=%v\n", e.Change, e.Phase, e.Repos)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to inspect")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	return cmd
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
