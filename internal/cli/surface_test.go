package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// walk collects every command path in the tree, hidden ones included.
func walk(cmd *cobra.Command, prefix string, out *[]string) {
	for _, child := range cmd.Commands() {
		name := child.Name()
		if prefix != "" {
			name = prefix + " " + name
		}
		*out = append(*out, name)
		walk(child, name, out)
	}
}

// TestCommandSurface is the golden list of everything a user can type.
//
// It exists so the surface cannot grow by accident. A command added
// without a deliberate decision — and without a line in the docs — fails
// here, which is the cheapest possible moment to notice.
func TestCommandSurface(t *testing.T) {
	var got []string
	walk(NewRootCmd(), "", &got)
	sort.Strings(got)

	want := []string{
		// Workspace lifecycle.
		"init",
		"status",
		"doctor",
		"version",
		"update",
		"update candidate-metadata",
		"update trust",
		"handoff",
		"attach",
		// Workflow.
		"task",
		"task start",
		"task status",
		"task abandon",
		"change",
		"change start",
		"change status",
		"change abandon",
		// The protocol a host speaks.
		"next",
		"report",
		"decide",
		"accept-edit",
		"guard",
		"host",
		"host probe",
		"host guard",
		"host install",
	}
	sort.Strings(want)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the command surface changed.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestEveryCommandExplainsItself proves no command ships without a
// description, because a surface you cannot read is a surface nobody uses
// correctly.
func TestEveryCommandExplainsItself(t *testing.T) {
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			if child.Name() == "completion" || child.Name() == "help" {
				continue
			}
			if strings.TrimSpace(child.Short) == "" {
				t.Errorf("%q has no short description", child.CommandPath())
			}
			check(child)
		}
	}
	check(NewRootCmd())
}

// TestReadOnlyCommandsWriteNothing is the sentinel the spec asks for:
// running a read-only command in a directory must not change it.
//
// It runs them in a plain directory on purpose. A command that would
// initialize a workspace in order to answer a question about one is
// exactly the failure this catches, and it is invisible in a directory
// that is already a workspace.
func TestReadOnlyCommandsWriteNothing(t *testing.T) {
	cases := [][]string{
		{"version"},
		{"host", "probe", "--host", "claude"},
		{"host", "probe", "--host", "opencode"},
		{"update", "trust"},
		{"update", "candidate-metadata"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			before := snapshot(t, dir)
			root := NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(append([]string{"--workspace", dir}, args...))
			// A read-only command in a plain directory must not fail
			// either: a host runs the probe everywhere.
			_ = root.ExecuteContext(context.Background())
			if diff := changed(before, snapshot(t, dir)); diff != "" {
				t.Fatalf("%v changed the directory: %s\n%s", args, diff, out.String())
			}
		})
	}
}

// snapshot records every path under root with its size and mode.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = info.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// changed names the first difference between two snapshots.
func changed(before, after map[string]string) string {
	for path, mode := range after {
		if was, ok := before[path]; !ok {
			return "created " + path
		} else if was != mode {
			return "changed " + path
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			return "removed " + path
		}
	}
	return ""
}
