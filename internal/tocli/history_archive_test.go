package tocli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func TestManagedTerminalUsesPreparedDateAcrossMidnight(t *testing.T) {
	for _, tc := range []struct {
		operation, phase string
		reuse            bool
	}{
		{"done", "do", false}, {"abandon", "plan", false},
		{"bypass", "abandoned", false}, {"bypass", "done", true},
		{"done", "done", true}, {"abandon", "abandoned", true},
	} {
		t.Run(tc.operation+"/"+tc.phase, func(t *testing.T) {
			l := explicitScopeWorkspace(t, "managed")
			run(t, false, "new", "clocked", "--repo", "api", "--dir", l.ConfigRoot)
			st, err := tostate.Load(statePath(l.ConfigRoot, "clocked"))
			if err != nil {
				t.Fatal(err)
			}
			st.Phase = tc.phase
			yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
			if st.Terminal() {
				st.Finished = yesterday
			}
			if err := tostate.Save(statePath(l.ConfigRoot, "clocked"), st); err != nil {
				t.Fatal(err)
			}
			if err := workspace.Checkpoint(l, nil, "seed phase"); err != nil {
				t.Fatal(err)
			}
			before, err := repoGitOutput(l.WorkflowRoot, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			oldToday := todayFn
			t.Cleanup(func() { todayFn = oldToday })
			var destination, selectedDate string
			root := &cobra.Command{Use: "to"}
			leaf := &cobra.Command{Use: tc.operation, RunE: func(cmd *cobra.Command, _ []string) error {
				var planned bool
				destination, selectedDate, planned, err = workspace.ArchiveTarget(cmd.Context(), changeDir(l.ConfigRoot, "clocked"))
				if err != nil || !planned {
					t.Fatalf("no prepared target: %v", err)
				}
				day, err := time.Parse("2006-01-02", selectedDate)
				if err != nil {
					t.Fatal(err)
				}
				// The real handler's clock crosses midnight after the wrapper has
				// prepared and journaled the operation. It must not reselect a target.
				todayFn = func() string { return day.AddDate(0, 0, 1).Format("2006-01-02") }
				switch tc.operation {
				case "done":
					return runDone(cmd, l.ConfigRoot, "clocked", true, "", false)
				case "abandon":
					return runAbandon(cmd, l.ConfigRoot, "clocked", false)
				default:
					return runBypass(cmd, l.ConfigRoot, "clocked", "archive", "test clock boundary")
				}
			}}
			leaf.Flags().String("dir", l.ConfigRoot, "")
			leaf.Flags().String("to", "archive", "")
			root.AddCommand(leaf)
			workspace.AttachHistory(root, "to")
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{tc.operation, "clocked"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if (selectedDate == yesterday) != tc.reuse {
				t.Fatalf("selected date %s; reuse yesterday=%t", selectedDate, tc.reuse)
			}
			archived, err := tostate.Load(filepath.Join(destination, tostate.FileName))
			if err != nil || archived.Finished != selectedDate {
				t.Fatalf("archive state=%+v, %v", archived, err)
			}
			if _, err := os.Lstat(changeDir(l.ConfigRoot, "clocked")); !os.IsNotExist(err) {
				t.Fatalf("active source remains: %v", err)
			}
			rel, err := filepath.Rel(l.WorkflowRoot, destination)
			if err != nil {
				t.Fatal(err)
			}
			paths, err := repoGitOutput(l.WorkflowRoot, "show", "--format=", "--name-only", "--no-renames", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(paths), "tasks/clocked/to-state.yaml") || !strings.Contains(string(paths), filepath.ToSlash(rel)+"/to-state.yaml") {
				t.Fatalf("archive adds/deletes not in one commit: %s", paths)
			}
			count, err := repoGitOutput(l.WorkflowRoot, "rev-list", "--count", strings.TrimSpace(string(before))+"..HEAD")
			if err != nil || strings.TrimSpace(string(count)) != "1" {
				t.Fatalf("commit count=%s, %v", count, err)
			}
			if dirt, err := repoGitOutput(l.WorkflowRoot, "status", "--porcelain"); err != nil || len(dirt) != 0 {
				t.Fatalf("unjournaled target: %s, %v", dirt, err)
			}
		})
	}
}
