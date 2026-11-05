package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/scaffold"
	"github.com/Novirmor/homonto/internal/starter"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	var sel starter.Selection
	var models, repos []string
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a new homonto repo, or generate a ready configuration from host, harness and engine selections",
		Long: `Without selections, init writes a commented homonto.toml scaffold.

With --harness and an --engine or --host, init writes a complete starting
configuration for that host × harness × engine selection (ADR 0110): the
packages, one [routing.default.<harness>] model per harness, the workflow
records root, and optionally [tmp] and sibling [repos]. It validates the
result before writing anything and never edits an existing homonto.toml.
Credentials never go in the file.`,
		Example: `  homonto init --harness opencode --engine onto --model opencode=anthropic/claude-sonnet-5-5
  homonto init --harness claude --host github --engine no --model claude=sonnet
  homonto init --harness opencode --harness claude --host gitlab --engine to \
    --model opencode=anthropic/claude-sonnet-5-5 --model claude=sonnet --tmp`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			selected := len(sel.Harnesses)+len(sel.Hosts)+len(sel.Engines)+len(models)+len(repos) > 0 || sel.Tmp || sel.Records != ""
			var created, updated []string
			var err error
			if !selected {
				created, updated, err = scaffold.Init(dir)
			} else {
				if sel.Models, err = splitPairs("--model", models); err != nil {
					return err
				}
				pairs, perr := splitPairList("--repo", repos)
				if perr != nil {
					return perr
				}
				sel.Repos = pairs
				doc, rerr := starter.Render(sel)
				if rerr != nil {
					return fmt.Errorf("init: %w", rerr)
				}
				if verr := validateGenerated(dir, doc); verr != nil {
					return verr
				}
				created, updated, err = scaffold.InitConfig(dir, doc)
			}
			if err != nil {
				return err
			}
			for _, p := range created {
				cmd.Println("created", p)
			}
			for _, p := range updated {
				cmd.Println("updated", p)
			}
			if selected {
				cmd.Println("next: review homonto.toml, then homonto plan and homonto apply")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&sel.Harnesses, "harness", nil, "harness to configure: opencode, claude (repeatable)")
	f.StringSliceVar(&sel.Hosts, "host", nil, "hosting workflow to configure: github, gitlab, bitbucket (repeatable)")
	f.StringSliceVar(&sel.Engines, "engine", nil, "engine to configure: onto, to, no (repeatable)")
	f.StringArrayVar(&models, "model", nil, "<harness>=<model>: the model every agent on that harness runs on (required per harness)")
	f.StringVar(&sel.Records, "records", "", `workflow records root (default "docs")`)
	f.BoolVar(&sel.Tmp, "tmp", false, "declare the [tmp] scratch directory (.tmp)")
	f.StringArrayVar(&repos, "repo", nil, "<alias>=<path>: a sibling Git repository (repeatable)")
	return cmd
}

func splitPairList(flag string, raw []string) ([][2]string, error) {
	var out [][2]string
	for _, r := range raw {
		k, v, ok := strings.Cut(r, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("init: %s %q is not <name>=<value>", flag, r)
		}
		out = append(out, [2]string{k, v})
	}
	return out, nil
}

func splitPairs(flag string, raw []string) (map[string]string, error) {
	pairs, err := splitPairList(flag, raw)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range pairs {
		if _, dup := out[p[0]]; dup {
			return nil, fmt.Errorf("init: %s %s=… is given twice", flag, p[0])
		}
		out[p[0]] = p[1]
	}
	return out, nil
}

// validateGenerated loads the generated config where it will live, so repo
// paths and the records root resolve as they will at plan, and removes it
// again. Nothing is left behind when the selection is refused.
func validateGenerated(dir, doc string) error {
	if _, err := os.Lstat(filepath.Join(dir, "homonto.toml")); err == nil {
		return fmt.Errorf("init: %s already exists; init never edits an existing config", filepath.Join(dir, "homonto.toml"))
	}
	tmp, err := os.CreateTemp(dir, ".homonto-init-*.toml")
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(doc); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("init: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if _, err := config.Load(tmp.Name()); err != nil {
		return fmt.Errorf("init: the selection does not produce a valid config, nothing was written: %w", err)
	}
	return nil
}
