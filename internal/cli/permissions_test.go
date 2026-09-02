package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// runCmdStdin executes a root subcommand feeding stdin bytes.
func runCmdStdin(t *testing.T, home, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// TestPermissionsSuggestRendersSnippet (A5): valid commands become a
// bash_allow_add snippet; unsafe, pattern, and credential-like commands are
// rejected inline; nothing is written anywhere.
func TestPermissionsSuggestRendersSnippet(t *testing.T) {
	home := t.TempDir()
	stdin := strings.Join([]string{
		"go test ./...",
		"git status",
		"rm -rf /tmp/x",
		"FOO=bar make test",
		"git *",
		"curl https://example.com/secret",
		"go test ./...", // duplicate, must render once
	}, "\n") + "\n"

	out, err := runCmdStdin(t, home, stdin, "permissions", "suggest")
	if err != nil {
		t.Fatalf("permissions suggest: %v\n%s", err, out)
	}
	if !strings.Contains(out, "bash_allow_add = [") {
		t.Fatalf("missing snippet header:\n%s", out)
	}
	if !strings.Contains(out, `"go test ./..."`) || !strings.Contains(out, `"git status"`) {
		t.Fatalf("valid commands missing:\n%s", out)
	}
	if strings.Count(out, `"go test ./..."`) != 1 {
		t.Fatalf("duplicate not deduplicated:\n%s", out)
	}
	// Unsafe commands must appear only inside "# rejected:" lines, never in
	// the rendered array.
	array := out[strings.Index(out, "bash_allow_add"):]
	for _, bad := range []string{`"rm -rf`, `"FOO=bar`, `"git *"`, `"curl`} {
		if strings.Contains(array, bad) {
			t.Errorf("unsafe command %q rendered into the array:\n%s", bad, array)
		}
	}
	// Nothing written to the filesystem.
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatalf("suggest wrote files: %v", entries)
	}
}

// TestPermissionsSuggestEmptyStdin: empty input yields a message, not an
// error and not a snippet.
func TestPermissionsSuggestEmptyStdin(t *testing.T) {
	out, err := runCmdStdin(t, t.TempDir(), "\n\n", "permissions", "suggest")
	if err != nil {
		t.Fatalf("empty stdin must not error: %v", err)
	}
	if strings.Contains(out, "bash_allow_add") {
		t.Fatalf("empty stdin rendered a snippet:\n%s", out)
	}
}