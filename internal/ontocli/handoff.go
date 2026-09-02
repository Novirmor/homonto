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
	"strings"

	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/spf13/cobra"
)

// handoffCmd builds "onto handoff <change> [--write] [--json]": a compact,
// recovery-oriented context pack for a change — identity, phase, deps, the
// pending gate, and pointers to the workspace artifacts with a content hash.
// After a context compaction the derivation recovers the *phase*; the handoff
// recovers *what the change is about* so a fresh agent can continue without
// re-reading everything.
//
// --json prints the interactive view (envelope plus the full native state) to
// stdout. --write persists the metadata-only recovery view (ADR 0027): a
// versioned JSON envelope and a Markdown pack rendered purely from envelope
// fields, never artifact prose.
func handoffCmd() *cobra.Command {
	var (
		dir     string
		doWrite bool
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "handoff <change>",
		Short: "Emit a compact recovery context pack for a change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			changeDir := filepath.Join(dir, "docs", "changes", name)
			st, err := ontostate.LoadChange(changeDir)
			if err != nil {
				return err
			}
			// The phase feeds the output filename; a malformed or traversal-
			// carrying value must never reach filepath.Join. Reject any value
			// outside the recognized phase set BEFORE --write builds a path from
			// it (F6). LoadChange does not Validate, so this is the gate.
			if !ontostate.ValidPhase(st.Phase) {
				return fmt.Errorf("onto handoff: %q has an unknown phase %q; refusing to build a handoff path from it", name, st.Phase)
			}
			pack, err := buildHandoff(name, changeDir, st)
			if err != nil {
				return err
			}
			if asJSON {
				rec, err := buildRecovery(cmd, dir, name, changeDir, st)
				if err != nil {
					return err
				}
				payload := struct {
					handoff.Recovery
					State        ontostate.State `json:"state"`
					SetCommands  []string        `json:"setCommands,omitempty"`
					ArtifactsAgg string          `json:"artifactsHash,omitempty"`
				}{Recovery: rec, State: st, ArtifactsAgg: pack.artifactsHash}
				for _, g := range pack.gates {
					payload.SetCommands = append(payload.SetCommands, g.SetCommand)
				}
				b, err := json.MarshalIndent(payload, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			if doWrite {
				rec, err := buildRecovery(cmd, dir, name, changeDir, st)
				if err != nil {
					return err
				}
				jsonBytes, err := json.MarshalIndent(rec, "", "  ")
				if err != nil {
					return err
				}
				outDir := filepath.Join(changeDir, ".onto", "handoff")
				jp, mp, err := handoff.WritePack(changeDir, outDir, rec, jsonBytes, []byte(handoff.Markdown(rec)))
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\nwrote %s\n", jp, mp)
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), pack.text)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().BoolVar(&doWrite, "write", false, "persist the metadata-only recovery pack under docs/changes/<name>/.onto/handoff/")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the interactive recovery view as JSON (full state; nothing is written)")
	return cmd
}

// handoffArtifacts are the workspace files the pack summarizes and hashes, in
// the order a reader wants them.
var handoffArtifacts = []string{"proposal.md", "notes.md", "design.md", "plan.md", "tasks.md", "verification.md"}

// textPack is the legacy stdout view: prose with excerpts. It never reaches a
// persisted file.
type textPack struct {
	text          string
	gates         []pendingGate
	artifactsHash string
}

func buildHandoff(name, changeDir string, st ontostate.State) (*textPack, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# onto handoff: %s\n\n", name)
	fmt.Fprintf(&b, "- **id**: %s\n- **workflow**: %s\n- **phase**: %s\n", nonEmpty(st.ID, "(none)"), nonEmpty(st.Workflow, "full"), st.Phase)
	if len(st.Deps) > 0 {
		fmt.Fprintf(&b, "- **deps**: %s\n", strings.Join(st.Deps, ", "))
	}
	if st.BaseRef != "" {
		fmt.Fprintf(&b, "- **base_ref**: %s\n", st.BaseRef)
	}

	gates := pendingGates(name, st)
	if len(gates) > 0 {
		b.WriteString("\n## Pending decision\n\n")
		for _, g := range gates {
			fmt.Fprintf(&b, "- **%s** — %s (`%s`)\n", g.Header, g.Question, g.SetCommand)
		}
	}

	b.WriteString("\n## Artifacts (present, with a content hash for staleness)\n\n")
	h := sha256.New()
	any := false
	for _, f := range handoffArtifacts {
		data, err := os.ReadFile(filepath.Join(changeDir, f))
		if err != nil {
			continue
		}
		any = true
		h.Write([]byte(f))
		h.Write(data)
		fmt.Fprintf(&b, "- `%s` — %s\n", f, firstMeaningfulLine(data))
	}
	if !any {
		b.WriteString("- (no artifacts yet)\n")
	}
	// Imported `to` provenance (ADR 0028): hash the imported workspace so a
	// promotion's source bytes stay verifiable from the pack.
	imported := hashTree(filepath.Join(changeDir, "imported-to"), "imported-to")
	for _, a := range imported {
		h.Write([]byte(a.Path))
		h.Write([]byte(a.SHA256))
		fmt.Fprintf(&b, "- `%s` — imported provenance\n", a.Path)
	}
	fmt.Fprintf(&b, "\n**artifacts-hash**: sha256:%x\n", h.Sum(nil))
	b.WriteString("\nRecover the phase from file state (the onto dispatcher's derivation); this pack is the *content* recovery. Re-read an artifact above if its excerpt is not enough.\n")
	return &textPack{text: b.String(), gates: gates, artifactsHash: hex.EncodeToString(h.Sum(nil))}, nil
}

// buildRecovery assembles the persisted-view envelope: identity, phases,
// deps, aliases, commits, gate IDs with argv templates, artifact digests, and
// a safe next argv. Free-form state stays out by construction.
func buildRecovery(cmd *cobra.Command, root, name, changeDir string, st ontostate.State) (handoff.Recovery, error) {
	ops := opid.New()
	rec := handoff.Recovery{
		SchemaVersion: handoff.SchemaVersion,
		Tool:          "onto",
		Change:        name,
		OperationID:   ops.NewID(),
		Workflow:      nonEmpty(st.Workflow, "full"),
		Phase:         st.Phase,
		Deps:          st.Deps,
		RepoAliases:   st.Repos,
		BaseRef:       st.BaseRef,
		HeadCommit:    headCommit(cmd, root),
	}
	handoff.Stamp(&rec, ops.Now())
	if _, err := st.DerivePhase(); err == nil {
		derived := ontostate.DeriveWorkingPhase(changeDir, st)
		rec.DerivedPhase = derived
		rec.PhaseMismatch = derived != st.Phase
	}
	for _, g := range pendingGates(name, st) {
		rec.PendingGates = append(rec.PendingGates, handoff.GateRef{ID: g.ID, Header: g.Header, SetArgv: g.SetArgv})
	}
	rec.Artifacts = digestArtifacts(changeDir)
	rec.NextArgv = nextArgv(name, st, rec.PendingGates)
	return rec, nil
}

// digestArtifacts hashes each present workspace artifact plus the imported-to
// provenance tree, sorted by path for determinism.
func digestArtifacts(changeDir string) []handoff.ArtifactDigest {
	var out []handoff.ArtifactDigest
	for _, f := range handoffArtifacts {
		data, err := os.ReadFile(filepath.Join(changeDir, f))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out = append(out, handoff.ArtifactDigest{Path: f, SHA256: hex.EncodeToString(sum[:])})
	}
	out = append(out, hashTree(filepath.Join(changeDir, "imported-to"), "imported-to")...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// hashTree digests every regular file under root, reporting slash-relative
// paths prefixed by rel. Missing root yields nil.
func hashTree(root, rel string) []handoff.ArtifactDigest {
	var out []handoff.ArtifactDigest
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		rp := rel + "/" + e.Name()
		if e.IsDir() {
			out = append(out, hashTree(p, rp)...)
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out = append(out, handoff.ArtifactDigest{Path: rp, SHA256: hex.EncodeToString(sum[:])})
	}
	return out
}

// nextArgv picks the safest command a fresh session should run: the first
// pending gate's recording template when a decision blocks, else the
// read-only phase re-ground.
func nextArgv(name string, st ontostate.State, gates []handoff.GateRef) []string {
	if len(gates) > 0 && len(gates[0].SetArgv) > 0 {
		return gates[0].SetArgv
	}
	return []string{"onto", "state", name, "--json"}
}

// headCommit resolves the workspace's current commit, read-only. Empty when
// git is absent or the root is not a repository — the envelope omits the
// field rather than failing the pack.
func headCommit(cmd *cobra.Command, root string) string {
	c := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	c.Stderr = nil
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// firstMeaningfulLine returns the first non-blank, non-heading, non-marker line
// of an artifact as a one-line excerpt.
func firstMeaningfulLine(data []byte) string {
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "---") {
			continue
		}
		if len(t) > 100 {
			t = t[:100] + "…"
		}
		return t
	}
	return "(empty)"
}
