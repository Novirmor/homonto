package ontocli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// graphNode is one node in the traceability graph — a change (kind "change",
// carrying id/phase/archived) or a capability (kind "capability", named in the
// Change field with id/phase empty).
type graphNode struct {
	ID        string `json:"id"`
	Change    string `json:"change"`
	Phase     string `json:"phase"`
	Archived  bool   `json:"archived"`
	Abandoned bool   `json:"abandoned"`
	Kind      string `json:"kind"`
}

// graphEdge is a typed relationship between changes. Edge types: "depends-on"
// (a change → each declared dep), "implements" (a change → each capability its
// delta specs touch), "supersedes" (a change → each change it replaces), and
// "deviates-from" (a change → each target it knowingly diverges from).
type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// graphCmd builds the "onto graph" subcommand: a read-only, config-independent
// view of the change dependency graph over active and archived changes.
func graphCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
		check  bool
	)
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Emit the change dependency graph (read-only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			nodes, edges, err := buildGraph(dir)
			if err != nil {
				return err
			}
			cycles := detectDepCycles(edges)
			if asJSON {
				b, mErr := json.MarshalIndent(struct {
					Nodes  []graphNode `json:"nodes"`
					Edges  []graphEdge `json:"edges"`
					Cycles [][]string  `json:"cycles"`
				}{Nodes: nodes, Edges: edges, Cycles: cycles}, "", "  ")
				if mErr != nil {
					return mErr
				}
				cmd.Println(string(b))
			} else {
				type outEdge struct{ typ, to string }
				edgesFrom := map[string][]outEdge{}
				for _, e := range edges {
					edgesFrom[e.From] = append(edgesFrom[e.From], outEdge{e.Type, e.To})
				}
				for _, n := range nodes {
					if n.Kind == "capability" {
						cmd.Printf("%s (capability)\n", n.Change)
						continue
					}
					suffix := ""
					if n.Archived {
						suffix = ", archived"
					}
					if n.Abandoned {
						suffix += ", abandoned"
					}
					id := n.ID
					if id == "" {
						id = "no-id"
					}
					cmd.Printf("%s (%s, %s%s)\n", n.Change, id, n.Phase, suffix)
					for _, e := range edgesFrom[n.Change] {
						cmd.Printf("  → %s %s\n", e.typ, e.to)
					}
				}
				if len(cycles) > 0 {
					cmd.Println("cycles:")
					for _, cyc := range cycles {
						cmd.Printf("  %s → %s\n", strings.Join(cyc, " → "), cyc[0])
					}
				}
			}
			// --check turns a detected cycle into a non-zero exit; without it, graph
			// stays a purely-informational read-only command (exit zero).
			if check && len(cycles) > 0 {
				return fmt.Errorf("onto graph: %d dependency cycle(s) detected", len(cycles))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to inspect")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit {nodes, edges, cycles} JSON")
	cmd.Flags().BoolVar(&check, "check", false, "exit non-zero if the dependency graph has a cycle")
	return cmd
}

// detectDepCycles finds cycles in the depends-on subgraph. Each returned cycle is
// an ordered list of change names forming the loop, rotated to start at its
// lexicographically smallest member and de-duplicated, so the result is
// deterministic regardless of directory read order. It reports a representative
// cycle for each cyclic strongly-connected region reached via a DFS back edge —
// enough to prove a build order does not exist. Only depends-on edges count.
func detectDepCycles(edges []graphEdge) [][]string {
	adj := map[string][]string{}
	nodeSet := map[string]bool{}
	for _, e := range edges {
		if e.Type != "depends-on" {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		nodeSet[e.From] = true
		nodeSet[e.To] = true
	}
	for k := range adj {
		sort.Strings(adj[k])
	}
	starts := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		starts = append(starts, n)
	}
	sort.Strings(starts)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	seen := map[string]bool{}
	var cycles [][]string

	var dfs func(u string)
	dfs = func(u string) {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range adj[u] {
			switch color[v] {
			case white:
				dfs(v)
			case gray:
				idx := -1
				for i, s := range stack {
					if s == v {
						idx = i
						break
					}
				}
				if idx >= 0 {
					norm := normalizeCycle(stack[idx:])
					key := strings.Join(norm, "\x00")
					if !seen[key] {
						seen[key] = true
						cycles = append(cycles, norm)
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
	}

	for _, n := range starts {
		if color[n] == white {
			dfs(n)
		}
	}
	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "\x00") < strings.Join(cycles[j], "\x00")
	})
	return cycles
}

// normalizeCycle rotates a cycle path to begin at its lexicographically smallest
// member, yielding one canonical representation per cycle. It copies the input.
func normalizeCycle(cyc []string) []string {
	min := 0
	for i := 1; i < len(cyc); i++ {
		if cyc[i] < cyc[min] {
			min = i
		}
	}
	out := make([]string, 0, len(cyc))
	out = append(out, cyc[min:]...)
	out = append(out, cyc[:min]...)
	return out
}

// buildGraph enumerates active (docs/changes/*) and archived
// (docs/changes/archive/*) changes into nodes + depends-on edges. A
// malformed/missing-state change still yields a node labeled by its directory
// (never silently dropped, mirroring status's F14 rule). Output is deterministic
// (nodes sorted by change name; edges by from,to).
func buildGraph(root string) ([]graphNode, []graphEdge, error) {
	var nodes []graphNode
	var edges []graphEdge

	capSeen := map[string]bool{}
	add := func(dir, fallbackName string, archived bool) {
		st, class, _ := ontostate.Classify(dir)
		name := st.Change
		if class != "valid" || name == "" {
			name = fallbackName
		}
		nodes = append(nodes, graphNode{ID: st.ID, Change: name, Phase: st.Phase, Archived: archived || st.Archived, Abandoned: st.Abandoned, Kind: "change"})
		for _, dep := range st.Deps {
			edges = append(edges, graphEdge{From: name, To: dep, Type: "depends-on"})
		}
		for _, sup := range st.Supersedes {
			edges = append(edges, graphEdge{From: name, To: sup, Type: "supersedes"})
		}
		for _, dev := range st.DeviatesFrom {
			edges = append(edges, graphEdge{From: name, To: dev, Type: "deviates-from"})
		}
		// implements: a change's delta specs live at specs/<capability>.md (onto's
		// flat delta-spec layout). Each names a capability the change implements.
		if specs, sErr := os.ReadDir(filepath.Join(dir, "specs")); sErr == nil {
			for _, sf := range specs {
				if sf.IsDir() || filepath.Ext(sf.Name()) != ".md" {
					continue
				}
				capName := sf.Name()[:len(sf.Name())-len(".md")]
				edges = append(edges, graphEdge{From: name, To: capName, Type: "implements"})
				if !capSeen[capName] {
					capSeen[capName] = true
					nodes = append(nodes, graphNode{Change: capName, Kind: "capability"})
				}
			}
		}
	}

	changesDir := changesDir(root)
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nodes, edges, nil // no changes: an empty graph, still read-only
		}
		return nil, nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" {
			continue
		}
		add(filepath.Join(changesDir, e.Name()), e.Name(), false)
	}
	if archived, aErr := os.ReadDir(filepath.Join(changesDir, "archive")); aErr == nil {
		for _, e := range archived {
			if !e.IsDir() {
				continue
			}
			add(filepath.Join(changesDir, "archive", e.Name()), e.Name(), true)
		}
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		return nodes[i].Change < nodes[j].Change
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return nodes, edges, nil
}
