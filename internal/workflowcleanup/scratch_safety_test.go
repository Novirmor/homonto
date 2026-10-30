package workflowcleanup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workflowcleanup"
)

func TestOldGenerationKeepsReusedNameScratch(t *testing.T) {
	for _, engine := range []string{"onto", "to"} {
		t.Run(engine, func(t *testing.T) {
			l := fixture(t)
			cfg, err := os.ReadFile(l.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			write(t, l.ConfigPath, string(cfg)+"[tmp]\ndir = '.tmp'\n")
			command(t, native(engine), "new", "repeat", "--repo", "source", "--dir", l.ConfigRoot)
			command(t, native(engine), "abandon", "repeat", "--dir", l.ConfigRoot)
			p := preview(t, l, engine, "repeat", "")
			if _, err := workflowcleanup.Apply(context.Background(), l, engine, "repeat", p.Identity, true); err != nil {
				t.Fatal(err)
			}
			command(t, native(engine), "new", "repeat", "--repo", "source", "--dir", l.ConfigRoot)
			path := filepath.Join(l.ConfigRoot, ".tmp", engine, "repeat", "new-work.md")
			write(t, path, "new generation's scratch\n")
			// Also protect the new generation after it becomes terminal: the old
			// identity still cannot establish ownership of this shared name.
			for _, terminal := range []bool{false, true} {
				if terminal {
					command(t, native(engine), "abandon", "repeat", "--dir", l.ConfigRoot)
				}
				if _, err := workflowcleanup.Apply(context.Background(), l, engine, "repeat", p.Identity, true); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("old generation removed reused-name scratch (terminal=%v): %v", terminal, err)
				}
			}
			// Once no generation is live, the explicit global sweep can remove it.
			if _, err := workflowcleanup.TmpClean(l, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("terminal scratch remains after sweep: %v", err)
			}
		})
	}
}

func TestTmpCleanRefusesIncompleteWorkflowInventory(t *testing.T) {
	for _, engine := range []string{"onto", "to"} {
		t.Run(engine, func(t *testing.T) {
			l := fixture(t)
			cfg, err := os.ReadFile(l.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			write(t, l.ConfigPath, string(cfg)+"[tmp]\ndir = '.tmp'\n")
			command(t, native(engine), "new", "live", "--repo", "source", "--dir", l.ConfigRoot)
			path := filepath.Join(l.ConfigRoot, ".tmp", engine, "live", "work.md")
			write(t, path, "active scratch\n")
			tree := "changes"
			if engine == "to" {
				tree = "tasks"
			}
			changes := filepath.Join(l.WorkflowRoot, tree)
			if err := os.Rename(changes, changes+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(changes+"-moved", changes); err != nil {
				t.Fatal(err)
			}
			for _, yes := range []bool{false, true} {
				r, err := workflowcleanup.TmpClean(l, yes)
				if err == nil {
					t.Fatalf("accepted failed inventory: %+v", r)
				}
				// The name lock may itself reject a symlinked parent before observation.
				if !strings.Contains(err.Error(), "inventory") && !strings.Contains(err.Error(), "symlink") {
					t.Fatalf("unexpected refusal: %v", err)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("deleted active scratch despite failed inventory: %v", err)
				}
			}
		})
	}
}
