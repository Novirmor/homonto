package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/engine"
	"github.com/Novirmor/homonto/internal/state"
	"github.com/spf13/cobra"
)

// explainKinds are the selectable resource kinds, mapped to their state-key
// prefixes. A kind may span several prefixes when the projection writes the
// same logical kind into different partitions.
var explainKinds = map[string][]string{
	"skill":        {"skill."},
	"command":      {"command."},
	"subagent":     {"subagent."},
	"subagentcopy": {"subagentcopy."},
	"setting":      {"setting."},
	"projsetting":  {"projsetting."},
	"tui":          {"tui."},
	"plugin":       {"plugin."},
}

// explainRow is one resource's story: what it is, who declared it, where it
// projects, what last touched it — or, for a tombstone, what removed it.
// Values are never included: a desired value can carry unresolved tokens and
// an applied value is a hash; neither answers "why is this here". Origin is
// the display string; OriginJSON is the structured view the JSON emit uses.
type explainRow struct {
	Kind        string      `json:"kind"`
	Name        string      `json:"name"`
	Key         string      `json:"key"`
	Tool        string      `json:"tool"`
	Repo        string      `json:"repo,omitempty"`
	Destination string      `json:"destination,omitempty"`
	Origin      string      `json:"-"`
	OriginJSON  *originJSON `json:"origin,omitempty"`
	LastEvent   *eventJSON  `json:"lastEvent,omitempty"`
	Removed     bool        `json:"removed,omitempty"`
}

type originJSON struct {
	Kind      string `json:"kind"`
	Framework string `json:"framework,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Source    string `json:"source,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type eventJSON struct {
	Op     string `json:"op"`
	Action string `json:"action"`
	Cause  string `json:"cause,omitempty"`
	At     string `json:"at"`
}

// stateFor returns the state partition an adapter label reads: the main state
// for "opencode", the named partition for "opencode@<repo>".
func stateFor(e *engine.Engine, tool string) *state.State {
	if name, ok := strings.CutPrefix(tool, "opencode@"); ok {
		for _, t := range e.RepoTargets {
			if t.Name == name {
				return t.State
			}
		}
	}
	return e.State
}

// partitionRepo extracts the repo alias from an adapter label, "" for main.
func partitionRepo(tool string) string {
	if name, ok := strings.CutPrefix(tool, "opencode@"); ok {
		return name
	}
	return ""
}

// buildRows joins descriptors (what is declared now) with state history (what
// happened) and tombstones (what was removed and why).
func buildRows(e *engine.Engine) []explainRow {
	rows := map[string]explainRow{}
	for _, r := range e.DescribeAll() {
		row := explainRow{
			Kind: r.Kind, Name: r.Name, Key: r.Key, Tool: r.Tool,
			Repo: partitionRepo(r.Tool), Destination: r.Destination,
		}
		if r.Origin != nil {
			row.OriginJSON = &originJSON{Kind: r.Origin.Kind, Framework: r.Origin.Framework, Provider: r.Origin.Provider, Source: r.Origin.Source, Scope: r.Origin.Scope, Repo: r.Origin.Repo}
			row.Origin = describeOrigin(r.Origin)
		} else {
			row.Origin = "unknown (predates provenance)"
		}
		if st := stateFor(e, r.Tool); st != nil {
			if entry, ok := st.Get(r.Tool, r.Key); ok {
				if entry.LastEvent != nil {
					row.LastEvent = &eventJSON{Op: entry.LastEvent.Op, Action: entry.LastEvent.Action, Cause: entry.LastEvent.Cause, At: entry.LastEvent.At}
				}
				if entry.Origin != nil && r.Origin == nil {
					row.OriginJSON = &originJSON{Kind: entry.Origin.Kind, Framework: entry.Origin.Framework, Provider: entry.Origin.Provider, Source: entry.Origin.Source, Scope: entry.Origin.Scope, Repo: entry.Origin.Repo}
					row.Origin = describeOrigin(entry.Origin)
				}
			}
		}
		rows[r.Tool+"\x00"+r.Key] = row
	}
	// Tombstones: resources no longer declared, with their removal record.
	for _, part := range append([]*state.State{e.State}, repoStates(e)...) {
		for _, t := range part.Tombstones {
			row := explainRow{
				Key: t.Key, Tool: t.Tool, Repo: partitionRepo(t.Tool), Removed: true,
				Origin: "(removed; declaration gone)", LastEvent: &eventJSON{Op: t.Op, Action: "delete", Cause: t.Cause, At: t.At},
			}
			if kind, name, ok := splitKey(t.Key); ok {
				row.Kind, row.Name = kind, name
			}
			rows[t.Tool+"\x00"+t.Key] = row
		}
	}
	out := make([]explainRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tool != out[j].Tool {
			return out[i].Tool < out[j].Tool
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func repoStates(e *engine.Engine) []*state.State {
	var out []*state.State
	for _, t := range e.RepoTargets {
		out = append(out, t.State)
	}
	return out
}

// splitKey recovers kind and name from a state key ("skill.foo" → "skill",
// "foo"). Config-supplied names may themselves contain dots; the first dot is
// always the kind separator, so the remainder is the name verbatim.
func splitKey(key string) (kind, name string, ok bool) {
	k, n, found := strings.Cut(key, ".")
	if !found {
		return "", "", false
	}
	return k, n, true
}

func describeOrigin(o *state.Origin) string {
	var b strings.Builder
	switch o.Kind {
	case "direct":
		b.WriteString("direct")
	case "framework":
		b.WriteString("framework:" + o.Framework)
		if o.Provider != "" && o.Provider != o.Framework {
			b.WriteString(" (provider " + o.Provider + ")")
		}
	default:
		b.WriteString("unknown")
	}
	if o.Scope != "" {
		b.WriteString(" scope=" + o.Scope)
	}
	if o.Repo != "" {
		b.WriteString(" repo=" + o.Repo)
	}
	if o.Source != "" {
		b.WriteString(" source=" + o.Source)
	}
	return b.String()
}

// kindPrefixesOf validates a selector kind and returns its key prefixes.
func kindPrefixesOf(kind string) ([]string, error) {
	prefixes, ok := explainKinds[kind]
	if !ok {
		kinds := make([]string, 0, len(explainKinds))
		for k := range explainKinds {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		return nil, fmt.Errorf("explain: unknown kind %q (valid: %s)", kind, strings.Join(kinds, ", "))
	}
	return prefixes, nil
}

func explainCmd() *cobra.Command {
	var (
		repoFlag string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "explain [kind] [name]",
		Short: "Explain managed resources: origin, destination, last change, removal",
		Long: "Explain why each managed resource exists: who declared it, where it " +
			"projects, which operation last touched it, and — for removed resources — " +
			"which operation removed them. Values are never shown; secrets cannot leak.\n\n" +
			"`explain settings [package]` instead lists each package's resolved settings " +
			"and process definitions, marking manifest defaults.\n\n" +
			"`explain routing [agent]` shows which pin or routing policy chose each " +
			"agent's model, and why an auto policy selected or fell back (ADR 0099, 0100).",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			var kind, name string
			if len(args) > 0 {
				kind = args[0]
			}
			if len(args) > 1 {
				name = args[1]
			}
			if kind == "settings" {
				return explainSettings(cmd, e, name, asJSON)
			}
			if kind == "routing" {
				return explainRouting(cmd, e, name, asJSON)
			}
			rows := buildRows(e)

			if kind != "" {
				prefixes, err := kindPrefixesOf(kind)
				if err != nil {
					return err
				}
				var filtered []explainRow
				for _, r := range rows {
					if r.Kind != kind && !hasAnyPrefix(r.Key, prefixes) {
						continue
					}
					filtered = append(filtered, r)
				}
				rows = filtered
			}
			if name != "" {
				var filtered []explainRow
				for _, r := range rows {
					if r.Name == name {
						filtered = append(filtered, r)
					}
				}
				if len(filtered) == 0 {
					return fmt.Errorf("explain: no managed %s named %q", nonEmptyDefault(kind, "resource"), name)
				}
				// Same name in several partitions is ambiguous without --repo.
				if repoFlag == "" {
					partitions := map[string]bool{}
					for _, r := range filtered {
						partitions[r.Tool] = true
					}
					if len(partitions) > 1 {
						var names []string
						for p := range partitions {
							names = append(names, p)
						}
						sort.Strings(names)
						return fmt.Errorf("explain: %q %s exists in several partitions (%s); pass --repo to disambiguate", name, nonEmptyDefault(kind, "resource"), strings.Join(names, ", "))
					}
				}
				if repoFlag != "" {
					var scoped []explainRow
					for _, r := range filtered {
						if r.Repo == repoFlag {
							scoped = append(scoped, r)
						}
					}
					if len(scoped) == 0 {
						return fmt.Errorf("explain: no %s %q in repo %q", nonEmptyDefault(kind, "resource"), name, repoFlag)
					}
					filtered = scoped
				}
				rows = filtered
			} else if repoFlag != "" {
				var filtered []explainRow
				for _, r := range rows {
					if r.Repo == repoFlag {
						filtered = append(filtered, r)
					}
				}
				rows = filtered
			}

			if len(rows) == 0 {
				cmd.Println("nothing managed matches")
				return nil
			}
			if asJSON {
				b, err := json.MarshalIndent(rows, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(b))
				return nil
			}
			for _, r := range rows {
				line := fmt.Sprintf("%-13s %-20s %s", r.Kind, r.Name, r.Origin)
				if r.Repo != "" {
					line += fmt.Sprintf("  [repo %s]", r.Repo)
				}
				if r.Destination != "" {
					line += "\n    dst: " + r.Destination
				}
				if r.LastEvent != nil {
					ev := fmt.Sprintf("%s (%s", r.LastEvent.Action, r.LastEvent.Op)
					if r.LastEvent.Cause != "" {
						ev += ", " + r.LastEvent.Cause
					}
					if r.LastEvent.At != "" {
						ev += " at " + r.LastEvent.At
					}
					ev += ")"
					tag := "last"
					if r.Removed {
						tag = "removed"
					}
					line += "\n    " + tag + ": " + ev
				}
				cmd.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "restrict to a declared repository partition")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the explanation as JSON")
	return cmd
}

func hasAnyPrefix(key string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

func nonEmptyDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// explainSettings prints the resolved package settings (ADR 0095).
func explainSettings(cmd *cobra.Command, e *engine.Engine, name string, asJSON bool) error {
	all, err := e.Cfg.ResolvedSettings()
	if err != nil {
		return err
	}
	packages := all[:0:0]
	for _, ps := range all {
		if name == "" || ps.Package == name {
			packages = append(packages, ps)
		}
	}
	if name != "" && len(packages) == 0 {
		return fmt.Errorf("explain: no installed package %q declares settings", name)
	}
	if asJSON {
		if name != "" {
			b, err := json.MarshalIndent(packages[0], "", "  ")
			if err != nil {
				return err
			}
			cmd.Println(string(b))
			return nil
		}
		b, err := json.MarshalIndent(packages, "", "  ")
		if err != nil {
			return err
		}
		cmd.Println(string(b))
		return nil
	}
	for _, line := range settingsLines(packages) {
		cmd.Println(line)
	}
	return nil
}

// settingsLines renders resolved settings, one line per setting and process.
func settingsLines(packages []config.PackageSettings) []string {
	var out []string
	for _, ps := range packages {
		for _, s := range ps.Settings {
			value := "unset"
			if s.Value != nil {
				value = catalog.Describe(s.Value)
			}
			line := fmt.Sprintf("setting: %s.%s.%s = %s", ps.Table, ps.Package, s.Name, value)
			if s.Default {
				line += " (default)"
			}
			out = append(out, line)
		}
		processes := make([]string, 0, len(ps.Processes))
		for process := range ps.Processes {
			processes = append(processes, process)
		}
		sort.Strings(processes)
		for _, process := range processes {
			out = append(out, fmt.Sprintf("process: %s.%s.%s", ps.Table, ps.Package, process))
		}
	}
	return out
}

// explainRouting prints each agent's model source (ADR 0099, ADR 0100).
func explainRouting(cmd *cobra.Command, e *engine.Engine, agent string, asJSON bool) error {
	report, err := e.Routes()
	if err != nil {
		return err
	}
	if agent != "" {
		var only []engine.ResolvedRoute
		for _, r := range report.Routes {
			if r.Agent == agent {
				only = append(only, r)
			}
		}
		if len(only) == 0 {
			return fmt.Errorf("explain: no rendered agent named %q", agent)
		}
		report.Routes = only
	}
	if asJSON {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		cmd.Println(string(b))
		return nil
	}
	for _, line := range routingLines(report.Routes) {
		cmd.Println(line)
	}
	switch report.Discovery {
	case "ok":
		cmd.Printf("discovery: ok (%d models)\n", report.Models)
	case "failed":
		cmd.Printf("discovery: failed (%s)\n", report.DiscoveryError)
	}
	return nil
}

// routingLines renders one line per agent: model, source and reason.
func routingLines(routes []engine.ResolvedRoute) []string {
	var out []string
	for _, r := range routes {
		var line string
		switch {
		case r.From == config.RouteFromPin:
			line = fmt.Sprintf("route: %s → %s (pinned by %s)", r.Agent, r.Pin.Model, r.Selector)
		case r.Selection != nil:
			model := r.Selection.Model
			if r.Selection.Variant != "" {
				model += " variant " + r.Selection.Variant
			}
			line = fmt.Sprintf("route: %s → %s (%s; %s)", r.Agent, model, r.Selector, r.Selection.Reason)
		default:
			line = fmt.Sprintf("route: %s → no model source", r.Agent)
		}
		if r.Note != "" {
			line += " — " + r.Note
		}
		out = append(out, line)
	}
	return out
}
