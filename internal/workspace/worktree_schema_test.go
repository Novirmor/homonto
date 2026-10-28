package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/schema"
)

func TestWorktreeRefusesUnsupportedOntoStateBeforeCreation(t *testing.T) {
	for _, kind := range []string{"unversioned", "schema-2", "future", "state-yaml", "fix", "tweak"} {
		t.Run(kind, func(t *testing.T) {
			l := wtFixture(t, "onto")
			path := filepath.Join(l.WorkflowRoot, "changes", "feature", "onto-state.yaml")
			data := string(wtRead(t, path))
			switch kind {
			case "unversioned":
				data = strings.Replace(data, "schema_version: 3\n", "", 1)
			case "schema-2":
				data = strings.Replace(data, "schema_version: 3", "schema_version: 2", 1)
			case "future":
				data = strings.Replace(data, "schema_version: 3", "schema_version: 999", 1)
			case "state-yaml":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(filepath.Dir(path), "state.yaml")
			default:
				data += "workflow: " + kind + "\n"
			}
			wtWrite(t, path, data)
			_, err := CreateWorktree(l, "onto", "feature", "a", "main", "work/unsupported")
			if err == nil {
				t.Fatal("created a worktree from unsupported state")
			}
			if kind == "future" {
				if !errors.Is(err, schema.ErrTooNew) {
					t.Fatalf("future schema error = %v", err)
				}
			} else if !strings.Contains(err.Error(), "release that created it") {
				t.Fatalf("missing recovery instruction: %v", err)
			}
			if got := string(wtRead(t, path)); got != data {
				t.Fatal("rejection changed state")
			}
			if _, err := os.Lstat(registryPath(l)); !os.IsNotExist(err) {
				t.Fatalf("rejection wrote registry: %v", err)
			}
			if _, err := worktreeGit(l.Repos["a"], "show-ref", "--verify", "refs/heads/work/unsupported"); err == nil {
				t.Fatal("rejection created source branch")
			}
		})
	}
}
