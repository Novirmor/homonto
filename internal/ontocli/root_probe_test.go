package ontocli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// A workspace path that crosses a symlinked directory reaches commands as
// its real path, so their symlinked-parent checks see a real root.
func TestRootResolvesSymlinkedDir(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd()
	var got string
	probe := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		got, _ = cmd.Flags().GetString("dir")
		return nil
	}}
	probe.Flags().String("dir", ".", "")
	root.AddCommand(probe)
	root.SetArgs([]string{"probe", "--dir", link})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := real; got != want {
		t.Fatalf("--dir = %q, want %q", got, want)
	}
}
