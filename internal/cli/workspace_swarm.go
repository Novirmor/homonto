package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Novirmor/homonto/internal/config"
	engineapi "github.com/Novirmor/homonto/internal/engine"
	"github.com/Novirmor/homonto/internal/swarm"
	"github.com/Novirmor/homonto/internal/worktask"
	"github.com/spf13/cobra"
)

// openSwarm builds the engine (so remote packages and routes resolve as at
// plan) and opens the swarm store over its workspace tasks.
func openSwarm(cmd *cobra.Command) (*swarm.Store, *engineapi.Engine, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, nil, err
	}
	home, _ := os.UserHomeDir()
	e, err := engineapi.Build(cmd.Context(), path, home, "homonto")
	if err != nil {
		return nil, nil, err
	}
	tasks, err := worktask.OpenConfig(e.Cfg, e.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	return &swarm.Store{Tasks: tasks}, e, nil
}

// agentModels is the model an agent is routed to on every harness the config
// projects to: the candidate's fixed model (ADR 0113).
func agentModels(e *engineapi.Engine, agent string) (map[string]string, error) {
	models := map[string]string{}
	for _, h := range config.HarnessIDs() {
		if !e.Cfg.TargetsHarness(h) {
			continue
		}
		report, err := e.RoutesFor(h)
		if err != nil {
			return nil, err
		}
		for _, r := range report.Routes {
			if r.Agent != agent {
				continue
			}
			switch {
			case r.Selection != nil && r.Selection.Model != "":
				models[h] = r.Selection.Model
			case r.Pin.Model != "":
				models[h] = r.Pin.Model
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("agent %q is not an installed agent with a routed model; see homonto explain routing", agent)
	}
	return models, nil
}

func interruptible(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	// A check or preview start runs child processes; turn the first
	// interrupt into cancellation so teardown still runs and is recorded.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	context.AfterFunc(ctx, stop)
	return ctx, stop
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func workspaceSwarmCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "swarm", Short: "Compare two or three candidate implementations of one brief, then deliver the chosen one"}
	cmd.AddCommand(swarmCreateCmd(), swarmInspectCmd(), swarmCheckCmd(), swarmCompareCmd(), swarmSelectCmd(), swarmPreviewCmd(), swarmCleanupCmd())
	return cmd
}

// swarmCreateCmd records the brief and allocates the candidates.
func swarmCreateCmd() *cobra.Command {
	var engine, briefFile string
	var repos, candidates []string
	create := &cobra.Command{Use: "create <name>", Short: "Record the brief and allocate one worktree per candidate from the same base (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, e, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		var specs []swarm.CandidateSpec
		for _, raw := range candidates {
			name, agent, ok := strings.Cut(raw, "=")
			if !ok || name == "" || agent == "" {
				return fmt.Errorf("--candidate %q: use NAME=AGENT, such as a=no-implementer", raw)
			}
			models, err := agentModels(e, agent)
			if err != nil {
				return fmt.Errorf("candidate %s: %w", name, err)
			}
			specs = append(specs, swarm.CandidateSpec{Name: name, Agent: agent, Models: models})
		}
		sw, err := s.Create(cmd.Context(), args[0], engine, repos, specs, briefFile)
		if err != nil {
			return err
		}
		return encode(cmd.OutOrStdout(), sw)
	}}
	create.Flags().StringVar(&engine, "engine", "no", "stateless engine the candidates run under")
	create.Flags().StringArrayVar(&repos, "repo", nil, "changed repository alias (repeatable)")
	create.Flags().StringArrayVar(&candidates, "candidate", nil, "NAME=AGENT, two or three times; the agent's routed model is the candidate's fixed model")
	create.Flags().StringVar(&briefFile, "brief", "", "file holding the brief every candidate receives")
	for _, flag := range []string{"repo", "candidate", "brief"} {
		_ = create.MarkFlagRequired(flag)
	}
	return create
}

// swarmInspectCmd shows the swarm record and its tasks.
func swarmInspectCmd() *cobra.Command {
	inspect := &cobra.Command{Use: "inspect <name>", Short: "Show the swarm record and each candidate's task (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		sw, tasks, warn, err := s.Load(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if warn != nil {
			cmd.PrintErrln("warning:", warn)
		}
		return encode(cmd.OutOrStdout(), struct {
			Swarm *swarm.Swarm              `json:"swarm"`
			Tasks map[string]*worktask.Task `json:"tasks"`
		}{sw, tasks})
	}}
	return inspect
}

// swarmCheckCmd runs the integration checks on every candidate.
func swarmCheckCmd() *cobra.Command {
	var checkName string
	var paused bool
	var timeout time.Duration
	check := &cobra.Command{Use: "check <name>", Short: "Run the same configured integration checks on every candidate; all candidate writers must be paused", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !paused {
			return fmt.Errorf("pause every candidate's writers, then pass --writers-paused")
		}
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		runs, err := s.Check(ctx, args[0], checkName, timeout, cmd.ErrOrStderr())
		if encErr := encode(cmd.OutOrStdout(), runs); encErr != nil {
			return encErr
		}
		if len(runs) == 0 && err == nil {
			cmd.PrintErrln("No configured check matches; verify each candidate yourself and select with --fallback-verified.")
		}
		return err
	}}
	check.Flags().StringVar(&checkName, "check", "", "run one matching check (default: all matching checks)")
	check.Flags().BoolVar(&paused, "writers-paused", false, "confirm every candidate's writers have been paused")
	check.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "time limit per check and candidate")
	return check
}

// swarmCompareCmd shows the candidates side by side.
func swarmCompareCmd() *cobra.Command {
	var jsonOut, htmlOut bool
	compare := &cobra.Command{Use: "compare <name>", Short: "Show the candidates side by side: model, change size, checks, previews", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		cmp, err := s.Compare(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if htmlOut {
			path, err := s.WriteHTML(cmp)
			if err != nil {
				return err
			}
			cmd.PrintErrln("side-by-side page:", path)
		}
		if jsonOut {
			return encode(cmd.OutOrStdout(), cmp)
		}
		return printComparison(cmd.OutOrStdout(), cmp)
	}}
	compare.Flags().BoolVar(&jsonOut, "json", false, "print the comparison as JSON")
	compare.Flags().BoolVar(&htmlOut, "html", false, "also write a side-by-side page that embeds running previews")
	return compare
}

// swarmSelectCmd records the chosen candidate.
func swarmSelectCmd() *cobra.Command {
	var winner string
	var fallback bool
	selectCmd := &cobra.Command{Use: "select <name>", Short: "Record the user's chosen candidate; only a verified candidate can be chosen (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		sw, err := s.Select(cmd.Context(), args[0], winner, fallback)
		if err != nil {
			return err
		}
		return encode(cmd.OutOrStdout(), sw.Selection)
	}}
	selectCmd.Flags().StringVar(&winner, "candidate", "", "the candidate the user chose")
	selectCmd.Flags().BoolVar(&fallback, "fallback-verified", false, "no configured check matches and the candidate passed the verification you ran")
	_ = selectCmd.MarkFlagRequired("candidate")
	return selectCmd
}

// swarmCleanupCmd tears the swarm down.
func swarmCleanupCmd() *cobra.Command {
	var yes, abandon, keepRejected bool
	cleanup := &cobra.Command{Use: "cleanup <name>", Short: "Tear down previews, remove every candidate worktree and delete the rejected branches (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		report, err := s.Cleanup(cmd.Context(), args[0], yes, abandon, keepRejected)
		if report != nil {
			if encErr := encode(cmd.OutOrStdout(), report); encErr != nil {
				return encErr
			}
		}
		return err
	}}
	cleanup.Flags().BoolVar(&yes, "yes", false, "confirm removal of the swarm's worktrees and previews")
	cleanup.Flags().BoolVar(&abandon, "abandon", false, "release every candidate without a winner, keeping all branches")
	cleanup.Flags().BoolVar(&keepRejected, "keep-rejected", false, "keep the rejected candidates' branches")
	return cleanup
}

func swarmPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "preview", Short: "Run each candidate in a local Docker container under resource budgets"}
	var only []string
	start := &cobra.Command{Use: "start <name>", Short: "Start (or restart) the configured previews for the candidates (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := interruptible(cmd)
		defer stop()
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		started, err := s.StartPreviews(ctx, args[0], only, cmd.ErrOrStderr())
		if started != nil {
			if encErr := encode(cmd.OutOrStdout(), started); encErr != nil {
				return encErr
			}
		}
		return err
	}}
	start.Flags().StringArrayVar(&only, "candidate", nil, "candidate to preview (repeatable; default: all)")
	var stopOnly []string
	stopCmd := &cobra.Command{Use: "stop <name>", Short: "Tear down the candidates' previews (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		stopped, err := s.StopPreviews(cmd.Context(), args[0], stopOnly)
		if err != nil {
			return err
		}
		return encode(cmd.OutOrStdout(), stopped)
	}}
	stopCmd.Flags().StringArrayVar(&stopOnly, "candidate", nil, "candidate whose previews to stop (repeatable; default: all)")
	var until string
	reap := &cobra.Command{Use: "reap <name>", Short: "Remove previews past their time budget, and anything an interrupted start left", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var at time.Time
		if until != "" {
			t, err := time.Parse(time.RFC3339, until)
			if err != nil {
				return fmt.Errorf("--until: %w", err)
			}
			at = t
		}
		s, _, err := openSwarm(cmd)
		if err != nil {
			return err
		}
		return s.Reap(cmd.Context(), args[0], at)
	}}
	reap.Flags().StringVar(&until, "until", "", "wait until this RFC 3339 time first (the expiry watchdog)")
	cmd.AddCommand(start, stopCmd, reap)
	return cmd
}

// printComparison renders one column per candidate.
func printComparison(w io.Writer, cmp *swarm.Comparison) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(label string, cell func(swarm.CandidateView) string) {
		cells := []string{label}
		for _, c := range cmp.Candidates {
			cells = append(cells, cell(c))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	row("candidate", func(c swarm.CandidateView) string {
		if c.Selected {
			return c.Name + " (selected)"
		}
		return c.Name
	})
	row("agent", func(c swarm.CandidateView) string { return c.Agent })
	harnesses := map[string]bool{}
	for _, c := range cmp.Candidates {
		for h := range c.Models {
			harnesses[h] = true
		}
	}
	for _, h := range sortedKeys(harnesses) {
		row("model "+h, func(c swarm.CandidateView) string { return c.Models[h] })
	}
	for _, alias := range cmp.Changed {
		row(alias, func(c swarm.CandidateView) string {
			d := c.Diff[alias]
			return fmt.Sprintf("%s %dc %df +%d -%d", short(c.Revisions[alias]), d.Commits, d.Files, d.Insertions, d.Deletions)
		})
	}
	for _, name := range cmp.Checks {
		row("check "+name, func(c swarm.CandidateView) string {
			v := c.Checks[name]
			if v.Stale {
				return v.Status + " (stale)"
			}
			return v.Status
		})
	}
	row("previews", func(c swarm.CandidateView) string {
		var parts []string
		for _, p := range c.Previews {
			if p.URL != "" {
				parts = append(parts, p.URL)
			} else {
				parts = append(parts, p.Preview+":"+p.State)
			}
		}
		if len(parts) == 0 {
			return "-"
		}
		return strings.Join(parts, " ")
	})
	row("verified", func(c swarm.CandidateView) string {
		if c.Verified {
			return "yes"
		}
		return "no"
	})
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, c := range cmp.Candidates {
		for _, r := range c.Unverified {
			fmt.Fprintf(w, "%s: %s\n", c.Name, r)
		}
	}
	if cmp.Warning != "" {
		fmt.Fprintln(w, "warning:", cmp.Warning)
	}
	return nil
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
