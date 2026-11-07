package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Novirmor/homonto/internal/capability"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/decision"
	"github.com/Novirmor/homonto/internal/engine"
	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	var (
		output   string
		exitFlag bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show config drift since last apply",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("status: --output %q is not one of text|json", output)
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			e.HomontoVersion = Version
			drift, pending, err := e.Status()
			if err != nil {
				return err
			}
			if exitFlag {
				setExitCode(statusExitCode(len(drift) > 0, pending))
			}
			routes, routed, err := e.AppliedRoutes()
			if err != nil {
				return err
			}
			// Recorded decision signals (ADR 0102): the latest calls, so a
			// workflow's judgments are visible next to its routes.
			decisions, torn, err := decision.Recent(e.StateDir, 20)
			if err != nil {
				return err
			}
			r := statusReport{drift: drift, pending: pending, warnings: append(e.OpenCodeV2Warnings(), e.Warnings...),
				routes: routes, routed: routed, decisions: decisions, torn: torn}
			if output == "json" {
				return printStatusJSON(cmd, r)
			}
			return printStatusText(cmd, r, e.Warnings)
		},
	}
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	cmd.Flags().BoolVar(&exitFlag, "exit-code", false, "exit 2 (pending) or 3 (drift) under the opt-in taxonomy")
	return cmd
}

// statusReport is what status found, before formatting.
type statusReport struct {
	drift     []string
	pending   int
	warnings  []string
	routes    engine.RoutingRecord
	routed    bool
	decisions []decision.Outcome
	torn      int
}

func printStatusJSON(cmd *cobra.Command, r statusReport) error {
	payload := struct {
		Drift     []string              `json:"drift"`
		Pending   int                   `json:"pending"`
		Warnings  []string              `json:"warnings"`
		Routing   *engine.RoutingRecord `json:"routing,omitempty"`
		Decisions []decision.Outcome    `json:"decisions,omitempty"`
	}{Drift: r.drift, Pending: r.pending, Warnings: r.warnings, Decisions: r.decisions}
	if r.routed {
		payload.Routing = &r.routes
	}
	if payload.Drift == nil {
		payload.Drift = []string{}
	}
	if payload.Warnings == nil {
		payload.Warnings = []string{}
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	cmd.Println(string(b))
	return nil
}

// printStatusText prints warnings, drift, the routes the last apply stamped
// into agents (ADR 0102; only policy-routed agents, since a pin is visible in
// the config), the latest decisions, and the verdict. skipped are the
// engine's own warnings, which make a clean verdict incomplete.
func printStatusText(cmd *cobra.Command, r statusReport, skipped []string) error {
	for _, w := range r.warnings {
		cmd.Println("warn:", w)
	}
	for _, l := range r.drift {
		cmd.Println(l)
	}
	if r.routed {
		for _, l := range appliedRouteLines(r.routes) {
			cmd.Println(l)
		}
	}
	decisions := r.decisions
	if len(decisions) > 5 {
		decisions = decisions[len(decisions)-5:]
	}
	for _, l := range decisionLines(decisions) {
		cmd.Println(l)
	}
	if r.torn > 0 {
		cmd.Printf("warn: %d unreadable line(s) in .homonto/%s\n", r.torn, decision.RecordFile)
	}
	if r.pending > 0 {
		cmd.Println(fmt.Sprintf("%d config change(s) awaiting apply (run `homonto apply`)", r.pending))
	}
	if len(r.drift) == 0 && r.pending == 0 {
		if err := coverageComplete(skipped); err != nil {
			return err
		}
		cmd.Println("No drift.")
	}
	return nil
}

// appliedRouteLines renders a routing record's policy-routed agents,
// harness by harness, opencode first. A route on another harness names its
// harness, since the same agent routes once per harness.
func appliedRouteLines(rec engine.RoutingRecord) []string {
	byHarness := rec.ByHarness()
	var out []string
	for _, harness := range config.HarnessIDs() {
		report, ok := byHarness[harness]
		if !ok {
			continue
		}
		var policyRouted []engine.ResolvedRoute
		for _, r := range report.Routes {
			if r.Selection != nil {
				policyRouted = append(policyRouted, r)
			}
		}
		prefix := ""
		if harness != "opencode" {
			prefix = harness + " "
		}
		for _, l := range routingLines(policyRouted) {
			out = append(out, prefix+l)
		}
		if report.Discovery == "failed" {
			out = append(out, fmt.Sprintf("%sroute discovery failed at last apply (%s): auto policies used their fallbacks", prefix, report.DiscoveryError))
		}
	}
	return out
}

func doctorCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check environment health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("doctor: --output %q is not one of text|json", output)
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			e.HomontoVersion = Version
			findings := e.Doctor()
			// Incomplete snapshot journals are the crash-recovery surface: a
			// SIGKILLed snapshot apply leaves a prepared journal that blocks
			// nothing but needs a decision. Surface it with the exact
			// recovery command (ADR 0030).
			if ids, err := e.IncompleteSnapshots(); err == nil {
				for _, id := range ids {
					findings = append(findings, fmt.Sprintf("incomplete snapshot apply %s — run `homonto snapshot recover %s`", id, id))
				}
			}
			if output == "json" {
				if findings == nil {
					findings = []string{}
				}
				b, merr := json.MarshalIndent(struct {
					Findings  []string            `json:"findings"`
					Harnesses []capability.Report `json:"harnesses"`
				}{Findings: findings, Harnesses: e.HarnessReports()}, "", "  ")
				if merr != nil {
					return merr
				}
				cmd.Println(string(b))
				return nil
			}
			for _, l := range findings {
				cmd.Println(l)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}
