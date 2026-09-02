package ontocli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/spf13/cobra"
)

// evidenceCmd groups the structured-record surface (ADR 0027): recording a
// verification claim and tracing requirements to evidence. The binary never
// executes verification commands — the agent runs them itself, because
// executing them through `onto` would ride the orchestrator's own Bash
// allowlist.
func evidenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Record and inspect structured verification evidence",
	}
	cmd.AddCommand(evidenceRecordCmd())
	return cmd
}

func evidenceRecordCmd() *cobra.Command {
	var (
		dir        string
		task       int
		scenario   string
		executable string
		cmdHash    string
		exit       int
		output     string
		artifact   string
		repo       string
	)
	cmd := &cobra.Command{
		Use:   "record <change>",
		Short: "Record a verification claim (hashes only; never executes anything)",
		Long: "Records that one scenario was verified by one command. The command is " +
			"named and hashed, never stored as argv; the output file is hashed, never " +
			"copied. The current commit anchors the claim. Run the command yourself — " +
			"onto must not execute verification, or it would bypass the orchestrator's " +
			"permission allowlist.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			if task <= 0 {
				return fmt.Errorf("evidence record: --task must be a positive tasks.md task number")
			}
			if scenario == "" {
				return fmt.Errorf("evidence record: --scenario is required (the Scenario-ID the claim backs)")
			}
			if executable == "" {
				return fmt.Errorf("evidence record: --exec is required (the executable that ran)")
			}
			if !isHexHash(cmdHash) {
				return fmt.Errorf("evidence record: --cmd-hash must be a sha256 hex digest of the command line")
			}
			changeDir := filepath.Join(dir, "docs", "changes", name)
			if _, err := os.Stat(filepath.Join(changeDir, "onto-state.yaml")); err != nil {
				return fmt.Errorf("evidence record: no change %q under %s", name, filepath.Join(dir, "docs", "changes"))
			}
			sc, _, err := evidence.Load(name, evidence.Path(changeDir))
			if err != nil {
				return err
			}
			if sc == nil {
				sc = evidence.New(name)
			}
			outHash := ""
			if output != "" {
				h, err := hashFile(output)
				if err != nil {
					return fmt.Errorf("evidence record: hashing --output: %w", err)
				}
				outHash = h
			}
			artHash := ""
			if artifact != "" {
				h, err := hashFile(artifact)
				if err != nil {
					return fmt.Errorf("evidence record: hashing --artifact: %w", err)
				}
				artHash = h
			} else if h, err := hashFile(filepath.Join(changeDir, "verification.md")); err == nil {
				artHash = h
			}
			ops := opid.New()
			rec := evidence.Record{
				Task: task, Scenario: scenario, Executable: executable, CommandHash: cmdHash,
				Repo: repo, Commit: headCommitAt(cmd, dir), OperationID: ops.NewID(),
				ExitStatus: exit, OutputHash: outHash, ArtifactHash: artHash,
				At: ops.Now().Format("2006-01-02T15:04:05Z"),
			}
			sc.Records = append(sc.Records, rec)
			if err := evidence.Save(changeDir, sc); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recorded %s <- task %d, %s, exit %d, commit %s\n", scenario, task, executable, exit, short(rec.Commit))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().IntVar(&task, "task", 0, "tasks.md task number the verification belongs to")
	cmd.Flags().StringVar(&scenario, "scenario", "", "Scenario-ID the claim backs")
	cmd.Flags().StringVar(&executable, "exec", "", "executable that ran (named only)")
	cmd.Flags().StringVar(&cmdHash, "cmd-hash", "", "sha256 hex digest of the command line")
	cmd.Flags().IntVar(&exit, "exit", 0, "the command's exit status")
	cmd.Flags().StringVar(&output, "output", "", "file whose content is the command output (hashed, never copied)")
	cmd.Flags().StringVar(&artifact, "artifact", "", "artifact the claim verifies (default: verification.md)")
	cmd.Flags().StringVar(&repo, "repo", "", "declared [repos] alias the command ran in (optional)")
	return cmd
}

// traceCmd builds "onto trace [change] [--json]": the requirement-to-evidence
// graph — capabilities, requirements, scenarios (with their stable IDs),
// tasks, commits, and evidence records as typed nodes and edges. The existing
// `onto graph` change-dependency view is untouched.
func traceCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "trace [change]",
		Short: "Trace requirements to scenarios, tasks, and evidence",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changesDir := filepath.Join(dir, "docs", "changes")
			names := activeChangeNames(cmd, changesDir)
			if len(args) == 1 {
				if err := ontoFramework.ValidChangeName(args[0]); err != nil {
					return err
				}
				names = []string{args[0]}
			}
			g := buildTrace(cmd, dir, changesDir, names)
			if asJSON {
				b, err := json.MarshalIndent(g, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			renderTrace(cmd, g)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the trace graph as JSON")
	return cmd
}

// traceGraph is the typed output: nodes keyed by kind:id, edges as typed
// pairs. Deterministic: nodes sorted by kind then id, edges sorted.
type traceGraph struct {
	Nodes []traceNode `json:"nodes"`
	Edges []traceEdge `json:"edges"`
}

type traceNode struct {
	Kind  string `json:"kind"` // change | capability | requirement | scenario | task | commit | evidence
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type traceEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // contains | requires | verified-by | recorded-at | executed-by | affects
}

func buildTrace(cmd *cobra.Command, root, changesDir string, names []string) traceGraph {
	g := traceGraph{}
	addNode := func(kind, id, label string) {
		g.Nodes = append(g.Nodes, traceNode{Kind: kind, ID: id, Label: label})
	}
	addEdge := func(from, to, kind string) {
		g.Edges = append(g.Edges, traceEdge{From: from, To: to, Kind: kind})
	}
	for _, name := range names {
		changeDir := filepath.Join(changesDir, name)
		addNode("change", name, name)
		// Delta specs carry requirements and scenarios.
		specs, _ := filepath.Glob(filepath.Join(changeDir, "specs", "*.md"))
		for _, spec := range specs {
			cap := strings.TrimSuffix(filepath.Base(spec), ".md")
			addNode("capability", cap, cap)
			addEdge("change:"+name, "capability:"+cap, "affects")
			data, err := os.ReadFile(spec)
			if err != nil {
				continue
			}
			for _, r := range evidence.ParseRequirements(string(data)) {
				rKey := r.Name
				if r.ID != "" {
					rKey = r.ID
				}
				addNode("requirement", rKey, r.Name)
				addEdge("capability:"+cap, "requirement:"+rKey, "contains")
				for _, s := range r.Scenarios {
					sKey := s.Name
					if s.ID != "" {
						sKey = s.ID
					}
					addNode("scenario", sKey, s.Name)
					addEdge("requirement:"+rKey, "scenario:"+sKey, "requires")
				}
			}
		}
		// Tasks.
		if data, err := os.ReadFile(filepath.Join(changeDir, "tasks.md")); err == nil {
			for _, t := range evidence.ParseTasks(string(data)) {
				id := fmt.Sprintf("%s#%d", name, t.Number)
				state := "open"
				if t.Checked {
					state = "done"
				}
				addNode("task", id, state)
				addEdge("change:"+name, "task:"+id, "contains")
			}
		}
		// Evidence sidecar.
		if sc, ok, err := evidence.Load(name, evidence.Path(changeDir)); err == nil && ok {
			for i, rec := range sc.Records {
				id := fmt.Sprintf("%s/e%d", name, i+1)
				addNode("evidence", id, fmt.Sprintf("%s exit=%d @%s", rec.Executable, rec.ExitStatus, short(rec.Commit)))
				addEdge("scenario:"+rec.Scenario, "evidence:"+id, "verified-by")
				addNode("commit", rec.Commit, short(rec.Commit))
				addEdge("evidence:"+id, "commit:"+rec.Commit, "recorded-at")
				addEdge("task:"+fmt.Sprintf("%s#%d", name, rec.Task), "evidence:"+id, "verified-by")
			}
		}
	}
	sort.Slice(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Kind != g.Nodes[j].Kind {
			return g.Nodes[i].Kind < g.Nodes[j].Kind
		}
		return g.Nodes[i].ID < g.Nodes[j].ID
	})
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		if g.Edges[i].To != g.Edges[j].To {
			return g.Edges[i].To < g.Edges[j].To
		}
		return g.Edges[i].Kind < g.Edges[j].Kind
	})
	return g
}

func renderTrace(cmd *cobra.Command, g traceGraph) {
	for _, n := range g.Nodes {
		label := n.Label
		if label != "" && label != n.ID {
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s (%s)\n", n.Kind, n.ID, label)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s\n", n.Kind, n.ID)
		}
	}
	if len(g.Edges) > 0 {
		fmt.Fprintln(cmd.OutOrStdout())
		for _, e := range g.Edges {
			fmt.Fprintf(cmd.OutOrStdout(), "%s --%s--> %s\n", e.From, e.Kind, e.To)
		}
	}
}

// activeChangeNames lists change directories under docs/changes (excluding
// archive) that look like changes; best-effort for trace.
func activeChangeNames(cmd *cobra.Command, changesDir string) []string {
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(changesDir, e.Name(), "onto-state.yaml")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// headCommitAt resolves HEAD in root, read-only; empty when git is absent.
func headCommitAt(cmd *cobra.Command, root string) string {
	c := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

var _ = strconv.Itoa
