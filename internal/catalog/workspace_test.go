package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workspace"
)

func TestRenderWorkspaceResolvedLayout(t *testing.T) {
	l := workspace.Layout{SchemaVersion: 2, ConfigPath: "/work/config home/custom.toml", ConfigRoot: "/work/config home", WorkflowRoot: "/records", GitMode: "managed", WorktreesDir: "/execution", Repos: map[string]string{"b": "/src/b", "a": "/src/a"}}
	body := RenderWorkspace(l)
	for _, want := range []string{
		"Config file: `/work/config home/custom.toml`", "Config root: `/work/config home`",
		"Workflow root: `/records`", "Workflow Git mode: `managed`", "WorktreesDir: `/execution`",
		"- `a`: `/src/a`\n- `b`: `/src/b`", "no implicit config repository",
		"homonto workspace inspect --config '/work/config home/custom.toml' --json",
		"homonto worktree list --config '/work/config home/custom.toml' --json",
		"homonto worktree create CHANGE --workflow onto --repo ALIAS --base BASE --branch BRANCH --config '/work/config home/custom.toml' --json",
		"homonto workspace checkpoint --config '/work/config home/custom.toml' --message 'Describe the workflow edits' --path changes/CHANGE",
		"onto status --dir '/work/config home'", "to status --dir '/work/config home'",
		"Pass that working directory explicitly", "do not select it", "create/list",
		"Native edit denies cover workflow paths reported by the host",
		"OpenCode v1.18.29 omits a move destination", "cannot be guaranteed blocked",
		"Coordinator ownership remains binding", "Routine scripts remain trusted execution, not a sandbox",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("reference missing %q:\n%s", want, body)
		}
	}
	if !bytes.Equal(body, RenderWorkspace(l)) {
		t.Fatal("layout render is not deterministic")
	}
	for _, obsolete := range []string{"onto --dir", "to --dir"} {
		if strings.Contains(string(body), obsolete) {
			t.Errorf("generated reference retained flag-first command %q", obsolete)
		}
	}
	existing := l
	existing.GitMode = "existing"
	if !strings.Contains(string(RenderWorkspace(existing)), "checkpoint command above requires managed mode and is not available here") {
		t.Fatal("existing mode must not advertise managed-only checkpoints as usable")
	}
	for _, change := range []struct {
		name string
		edit func(*workspace.Layout)
	}{
		{"config file", func(l *workspace.Layout) { l.ConfigPath = "/work/config home/other.toml" }},
		{"config root", func(l *workspace.Layout) { l.ConfigRoot = "/elsewhere" }},
		{"workflow root", func(l *workspace.Layout) { l.WorkflowRoot = "/other-records" }},
		{"git mode", func(l *workspace.Layout) { l.GitMode = "existing" }},
		{"worktrees parent", func(l *workspace.Layout) { l.WorktreesDir = "/other-execution" }},
		{"no worktrees parent", func(l *workspace.Layout) { l.WorktreesDir = "" }},
		{"source path", func(l *workspace.Layout) { l.Repos = map[string]string{"a": "/src/other", "b": "/src/b"} }},
		{"source alias", func(l *workspace.Layout) { l.Repos = map[string]string{"renamed": "/src/a", "b": "/src/b"} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			other := l
			change.edit(&other)
			if bytes.Equal(body, RenderWorkspace(other)) {
				t.Fatal("resolved render ignored layout change; byte fingerprint would not change")
			}
		})
	}
	l.ConfigPath = "/config's/$file.toml"
	if !strings.Contains(string(RenderWorkspace(l)), `--config '/config'"'"'s/$file.toml'`) {
		t.Fatal("command arguments must be shell quoted")
	}
	l.ConfigRoot = "/config's/$workspace"
	for _, command := range []string{"onto", "to"} {
		if !strings.Contains(string(RenderWorkspace(l)), command+` status --dir '/config'"'"'s/$workspace'`) {
			t.Errorf("%s working directory must stay shell quoted after command reordering", command)
		}
	}
	for _, version := range []int{0, 1} {
		l.SchemaVersion = version
		if len(RenderWorkspace(l)) != 0 {
			t.Fatalf("schema %d must retain legacy projection", version)
		}
	}
}

func TestMaterializeWorkspaceReferencePlacementAndRemoval(t *testing.T) {
	c, root := baseCatalog(t), t.TempDir()
	names := []string{"homonto", "onto", "to", "onto-build", "h-resolve-issue"}
	ref := RenderWorkspace(workspace.Layout{SchemaVersion: 2, ConfigPath: "/cfg/custom.toml", ConfigRoot: "/cfg", WorkflowRoot: "/records", GitMode: "existing"})
	if err := c.Materialize(root, names, "none", "none", "", ref); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, name, WorkspaceReferencePath))
		if HasWorkspaceReference(name) {
			if err != nil || !bytes.Equal(data, ref) {
				t.Errorf("%s reference = %q, %v", name, data, err)
			}
		} else if !os.IsNotExist(err) {
			t.Errorf("%s must use the shared entry-point reference, got %v", name, err)
		}
	}
	if err := c.Materialize(root, names, "none", "none", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(root, name, WorkspaceReferencePath)); !os.IsNotExist(err) {
			t.Errorf("%s retained a stale workspace reference: %v", name, err)
		}
	}
}
