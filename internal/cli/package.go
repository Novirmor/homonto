package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
	toml "github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

func packageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "package", Short: "Work with homonto packages (engines, workflows, services)"}
	cmd.AddCommand(packageCheckCmd(), packageContractsCmd())
	return cmd
}

// packageContractsCmd publishes the contract versions this binary implements
// (ADR 0108), so a package author can declare [compat].contract against them.
func packageContractsCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "contracts",
		Short: "Print the package contract versions this homonto implements",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kinds := slices.Sorted(maps.Keys(catalog.Contracts))
			switch output {
			case "json":
				out := struct {
					ManifestSchema int               `json:"manifest_schema"`
					ConfigSchema   int               `json:"config_schema"`
					Contracts      map[string]string `json:"contracts"`
				}{catalog.ContractManifestSchema, config.CurrentConfigSchemaVersion, map[string]string{}}
				for _, k := range kinds {
					out.Contracts[k] = catalog.Contracts[k].String()
				}
				b, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(b))
			case "text":
				cmd.Printf("manifest_schema %d\n", catalog.ContractManifestSchema)
				cmd.Printf("config schema_version %d\n", config.CurrentConfigSchemaVersion)
				for _, k := range kinds {
					cmd.Printf("%s contract %s\n", k, catalog.Contracts[k])
				}
			default:
				return fmt.Errorf("package contracts: --output %q is not one of text|json", output)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
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
			cl, names, err := packagesToCheck(dirs, builtin)
			if err != nil {
				return err
			}
			slices.Sort(names)
			failed := 0
			for _, name := range names {
				r, err := cl.Conformance(name)
				if err != nil {
					return err
				}
				if !r.Passed() {
					failed++
				}
				printConformance(cmd, r)
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

// packagesToCheck loads each named package root over the catalog and lists
// what to check: those roots, plus with builtin every shipped package they do
// not replace.
func packagesToCheck(dirs []string, builtin bool) (*catalog.Catalog, []string, error) {
	locals := map[string]fs.FS{}
	var names []string
	for _, dir := range dirs {
		name, err := manifestName(dir)
		if err != nil {
			return nil, nil, err
		}
		if _, dup := locals[name]; dup {
			return nil, nil, fmt.Errorf("package check: two directories declare package %q", name)
		}
		locals[name] = os.DirFS(dir)
		names = append(names, name)
	}
	cl, err := catalog.NewWithLocal(locals)
	if err != nil {
		return nil, nil, fmt.Errorf("package check: %w", err)
	}
	if builtin {
		for _, name := range cl.PackageNames() {
			if _, local := locals[name]; !local {
				names = append(names, name)
			}
		}
	}
	return cl, names, nil
}

// printConformance prints one package's verdict and each failed check.
func printConformance(cmd *cobra.Command, r catalog.Conformance) {
	status := "ok"
	if !r.Passed() {
		status = "FAIL"
	}
	contract := ""
	if r.Contract != "" {
		contract = ", contract " + r.Contract
	}
	cmd.Printf("%s %s (%s %s%s)\n", status, r.Package, nonEmptyDefault(r.Kind, "no kind"), r.Version, contract)
	for _, c := range r.Checks {
		if c.Err != nil {
			cmd.Printf("  %s: %v\n", c.Name, c.Err)
		}
	}
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
