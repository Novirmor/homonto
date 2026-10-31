package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// A workspace path that crosses a symlinked directory reaches commands as
// its real path, so their symlinked-parent checks see a real root.
func TestRootResolvesSymlinkedConfigDir(t *testing.T) {
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
		got, _ = cmd.Flags().GetString("config")
		return nil
	}}

	root.AddCommand(probe)
	root.SetArgs([]string{"probe", "--config", filepath.Join(link, "homonto.toml")})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "homonto.toml"); got != want {
		t.Fatalf("--config = %q, want %q", got, want)
	}
}

// A symlinked homonto.toml (a dotfiles checkout) keeps its own directory as
// the project root; only directories resolve.
func TestRootKeepsSymlinkedConfigFile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dotfiles := filepath.Join(base, "dotfiles", "homonto.toml")
	if err := os.MkdirAll(filepath.Dir(dotfiles), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dotfiles, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "project", "homonto.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, config); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd()
	var got string
	root.AddCommand(&cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		got, _ = cmd.Flags().GetString("config")
		return nil
	}})
	root.SetArgs([]string{"probe", "--config", config})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got != config {
		t.Fatalf("--config = %q, want %q", got, config)
	}
}
