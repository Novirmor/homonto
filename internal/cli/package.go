package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Novirmor/homonto/internal/catalog"
	toml "github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

func packageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "package", Short: "Work with homonto packages (engines, workflows, services)"}
	cmd.AddCommand(packageCheckCmd())
	return cmd
}

// packageCheckCmd checks package roots against the public contract
// (docs/contracts/). Roots load together, so a workflow and the non-builtin
// service it requires are checked as a set, exactly as a config would
// install them.
func packageCheckCmd() *cobra.Command {
	var builtin bool
	cmd := &cobra.Command{
		Use:   "check [DIR...]",
		Short: "Check package roots (and with --builtin, the shipped packages) against the package contract",
		RunE: func(cmd *cobra.Command, dirs []string) error {
			if len(dirs) == 0 && !builtin {
				return fmt.Errorf("package check: name at least one package directory, or pass --builtin")
			}
			locals := map[string]fs.FS{}
			var names []string
			for _, dir := range dirs {
				name, err := manifestName(dir)
				if err != nil {
					return err
				}
				if _, dup := locals[name]; dup {
					return fmt.Errorf("package check: two directories declare package %q", name)
				}
				locals[name] = os.DirFS(dir)
				names = append(names, name)
			}
			cl, err := catalog.NewWithLocal(locals)
			if err != nil {
				return fmt.Errorf("package check: %w", err)
			}
			if builtin {
				for _, name := range cl.PackageNames() {
					if _, local := locals[name]; !local {
						names = append(names, name)
					}
				}
			}
			slices.Sort(names)
			failed := 0
			for _, name := range names {
				r, err := cl.Conformance(name)
				if err != nil {
					return err
				}
				status := "ok"
				if !r.Passed() {
					status = "FAIL"
					failed++
				}
				cmd.Printf("%s %s (%s %s)\n", status, r.Package, nonEmptyDefault(r.Kind, "no kind"), r.Version)
				for _, c := range r.Checks {
					if c.Err != nil {
						cmd.Printf("  %s: %v\n", c.Name, c.Err)
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("package check: %d of %d packages do not conform", failed, len(names))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&builtin, "builtin", false, "also check every package shipped in the catalog")
	return cmd
}

// manifestName reads the package name a directory's framework.toml declares.
func manifestName(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "framework.toml"))
	if err != nil {
		return "", fmt.Errorf("package check: %s: %w", dir, err)
	}
	var m struct {
		Name string `toml:"name"`
	}
	if err := toml.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("package check: %s/framework.toml: %w", dir, err)
	}
	if m.Name == "" {
		return "", fmt.Errorf("package check: %s/framework.toml declares no name", dir)
	}
	return m.Name, nil
}
