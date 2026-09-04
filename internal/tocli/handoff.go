package tocli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/spf13/cobra"
)

// Excerpt caps: a short plan is carried whole; a long plan is cut to its head
// (the goal) plus every still-unchecked task — the part a resuming session
// actually needs — rather than its first N lines (which, mid-do, are mostly
// finished history).
const (
	planExcerptLines = 60
	planHeadLines    = 20
	planSectionLines = 20
)

// excerptPlan reduces plan.md content to a resume-sized excerpt.
func excerptPlan(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= planExcerptLines {
		return strings.TrimRight(content, "\n")
	}

	blocks := planTaskBlocks(lines)

	// Do not cut a task contract in half at the head boundary.
	headEnd := planHeadLines
	for _, block := range blocks {
		if block.start < headEnd && block.end > headEnd {
			headEnd = block.start
			break
		}
	}
	out := append([]string{}, lines[:headEnd]...)
	out = append(out, "… (plan.md truncated; unchecked tasks below)")
	for _, block := range blocks {
		if block.end <= headEnd || !uncheckedTask.MatchString(lines[block.start]) {
			continue
		}
		out = append(out, lines[block.start:block.end]...)
	}
	for i, line := range lines {
		if i >= headEnd && strings.HasPrefix(strings.TrimSpace(line), finalVerifyLabel) {
			out = append(out, line)
		}
	}
	out = appendPlanSection(out, lines, headEnd, "## Notes")
	out = appendPlanSection(out, lines, headEnd, "## Verification")
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func appendPlanSection(out, lines []string, headEnd int, heading string) []string {
	for start, line := range lines {
		if strings.TrimSpace(line) != heading {
			continue
		}
		end := start + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "## ") {
			end++
		}
		if end <= headEnd {
			return out
		}
		sectionEnd := min(end, start+1+planSectionLines)
		out = append(out, lines[start:sectionEnd]...)
		if sectionEnd < end {
			out = append(out, fmt.Sprintf("… (%s truncated)", heading))
		}
		return out
	}
	return out
}

// handoffCmd builds "to handoff <change-name>": a compact context-recovery
// pack (identity, phase, plan excerpt, the next skill) for continuing
// after a context compaction. Read-only and config-independent.
//
// --json keeps the legacy keys (change/state/plan/next) and adds the
// versioned recovery-envelope fields (ADR 0027) so a machine consumer can
// re-ground without parsing prose. --write persists the metadata-only
// recovery view (JSON + markdown, unique operation-ID filenames).
func handoffCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
		doWrite  bool
	)

	cmd := &cobra.Command{
		Use:   "handoff <change-name>",
		Short: "Print a compact recovery pack for a change (read-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHandoff(cmd, dir, args[0], jsonMode, doWrite)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	cmd.Flags().BoolVar(&doWrite, "write", false, "persist the metadata-only recovery pack under <workflow-root>/tasks/<name>/.to/handoff/")
	return cmd
}

// nextStep maps a phase and plan progress to the skill that safely continues
// the change. It never recommends the bookkeeping command directly.
func nextStep(name, phase, plan string) string {
	switch phase {
	case tostate.PhasePlan:
		return fmt.Sprintf("continue planning with `/to-plan` for %s", name)
	case tostate.PhaseDo:
		if hasUncheckedTask(plan) {
			return fmt.Sprintf("resume implementation with `/to-do` for %s", name)
		}
		return fmt.Sprintf("all plan tasks are checked; finish with `/to-done` for %s", name)
	default:
		return "none — the change is terminal"
	}
}

func runHandoff(cmd *cobra.Command, root, name string, jsonMode, doWrite bool) error {
	st, err := loadChange(root, name)
	if err != nil {
		return err
	}

	b, err := os.ReadFile(planPath(root, name))
	if err != nil {
		return fmt.Errorf("to handoff: reading %s: %w", planPath(root, name), err)
	}
	plan := string(b)
	excerpt := excerptPlan(plan)
	next := nextStep(name, st.Phase, plan)

	if doWrite {
		rec := buildToRecovery(name, st, plan)
		jsonBytes, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return err
		}
		changeDir := changeDir(root, name)
		jp, mp, err := handoff.WritePack(changeDir, filepath.Join(changeDir, ".to", "handoff"), rec, jsonBytes, []byte(handoff.Markdown(rec)))
		if err != nil {
			return fmt.Errorf("to handoff: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\nwrote %s\n", jp, mp)
		return nil
	}

	if jsonMode {
		return printJSON(cmd, map[string]any{
			"schemaVersion": handoff.SchemaVersion,
			"tool":          "to",
			"change":        name,
			"operationId":   recOperationID(name, st, plan),
			"generated":     opid.New().Now().Format(time.RFC3339),
			"phase":         st.Phase,
			"derivedPhase":  st.Phase,
			"repoAliases":   st.Repos,
			"artifacts":     toArtifactDigests(plan),
			"nextArgv":      toNextArgv(name, st.Phase, plan),
			// Legacy keys, unchanged for existing consumers.
			"state": st,
			"plan":  excerpt,
			"next":  next,
		})
	}

	cmd.Printf("change: %s\nphase: %s\ncreated: %s\nnext: %s\n", name, st.Phase, st.Created, next)
	if excerpt != "" {
		cmd.Printf("\nplan.md:\n%s\n", excerpt)
	}
	return nil
}

// buildToRecovery assembles the persisted recovery view: identity, phase,
// repo aliases, the plan's digest, and a safe next argv. The plan excerpt and
// evidence text stay out — persisted packs are metadata only (ADR 0027).
func buildToRecovery(name string, st tostate.State, plan string) handoff.Recovery {
	ops := opid.New()
	rec := handoff.Recovery{
		SchemaVersion: handoff.SchemaVersion,
		Tool:          "to",
		Change:        name,
		OperationID:   ops.NewID(),
		Phase:         st.Phase,
		DerivedPhase:  st.Phase,
		RepoAliases:   st.Repos,
		Artifacts:     toArtifactDigests(plan),
		NextArgv:      toNextArgv(name, st.Phase, plan),
	}
	handoff.Stamp(&rec, ops.Now())
	return rec
}

func toArtifactDigests(plan string) []handoff.ArtifactDigest {
	sum := sha256.Sum256([]byte(plan))
	return []handoff.ArtifactDigest{{Path: "plan.md", SHA256: hex.EncodeToString(sum[:])}}
}

// toNextArgv picks the safest binary command for a fresh session: the phase
// advance when planning is done, else the read-only status re-ground.
func toNextArgv(name, phase, plan string) []string {
	switch phase {
	case tostate.PhasePlan:
		return []string{"to", "phase", name}
	case tostate.PhaseDo:
		if !hasUncheckedTask(plan) {
			return []string{"to", "status", "--json"}
		}
	}
	return []string{"to", "status", "--json"}
}

// recOperationID derives a stable per-invocation ID for the interactive view.
func recOperationID(name string, st tostate.State, plan string) string {
	return opid.New().NewID()
}
