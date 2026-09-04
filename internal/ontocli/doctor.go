package ontocli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// ErrQuietFindings is what `onto doctor --quiet` returns when there are
// findings: the caller (cmd/onto/main.go) must exit non-zero WITHOUT printing.
// Aliased to the shared workcli sentinel so the quiet contract and the
// errors.Is check in cmd/onto/main.go hold for both workflow CLIs from one
// definition.
var ErrQuietFindings = workcli.ErrQuietFindings

// doctorCmd builds the "onto doctor" subcommand: a strictly read-only,
// config-independent workspace-health diagnostic. Unlike init/new/close it is
// NOT gated on the framework install — a missing docs layout is a finding, not
// a refusal. It writes nothing and imports none of homonto's projection
// packages.
func doctorCmd() *cobra.Command {
	var (
		dir   string
		quiet bool
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report onto workflow/project health (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if quiet {
				// Hook-friendly: suppress ALL output, communicate only via exit
				// code (non-zero when there are findings). Used by an editor/tool
				// Stop hook to fail loudly on a workflow-integrity problem.
				// SilenceErrors/SilenceUsage stop cobra's own printing; main.go
				// additionally recognizes the quiet sentinel so its error line is
				// suppressed too — --quiet previously still leaked
				// "error: onto doctor: N problem(s) found" to stderr.
				cmd.SetOut(io.Discard)
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
				if err := runDoctor(cmd, dir); err != nil {
					return ErrQuietFindings
				}
				return nil
			}
			return runDoctor(cmd, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to inspect")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing; signal health via exit code only (for hooks)")
	return cmd
}

// runDoctor accumulates health findings in a fixed order — docs layout, active
// changes, archive layout — printing each to stdout. It performs zero writes
// and never calls gate(). On a healthy workspace it prints "healthy" and
// returns nil; otherwise it prints every finding and returns a summary error so
// main exits non-zero.
func runDoctor(cmd *cobra.Command, root string) error {
	var findings []string

	// 1. docs layout: every directory in the resolved workflow root must exist.
	for i, d := range workflowLayout {
		info, err := os.Stat(filepath.Join(workflowRoot(root), d))
		if err != nil || !info.IsDir() {
			label := filepath.Join(workflowRoot(root), d)
			if workflowRoot(root) == filepath.Join(root, "docs") {
				label = docsLayout[i]
			}
			findings = append(findings, "docs layout: missing directory "+label)
		}
	}

	// 2. active changes: enumerate change directories first (excluding
	// archive/), then classify. A missing-state or malformed directory is a
	// finding — a deleted state file is reported, never silently skipped (F14).
	changesDir := changesDir(root)
	if entries, readErr := os.ReadDir(changesDir); readErr == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "archive" {
				continue
			}
			name := e.Name()
			changeDir := filepath.Join(changesDir, name)
			st, class, classErr := ontostate.Classify(changeDir)
			switch class {
			case "missing-state":
				findings = append(findings, name+": missing-state (change directory has no state file)")
				continue
			case "malformed":
				findings = append(findings, fmt.Sprintf("%s: malformed state: %v", name, classErr))
				continue
			}
			// An abandoned change is a parked terminal state, not a health
			// problem: its missing artifacts, unresolved deps, and verify-round
			// count are exactly why it was abandoned. Counting them made a
			// `doctor --quiet` Stop hook fail forever with no clearing action.
			if st.Abandoned {
				continue
			}
			phase := st.Phase
			if skErr := ontostate.ValidateSkeleton(changeDir); skErr != nil {
				findings = append(findings, fmt.Sprintf("%s: phase %s missing artifact: %v", name, phase, skErr))
			}
			if unresolved := ontostate.DepsResolved(root, st.Deps); len(unresolved) > 0 {
				findings = append(findings, fmt.Sprintf("%s: unresolved dependencies: %v", name, unresolved))
			}
			if st.Archived {
				findings = append(findings, name+": active change marked archived: true (belongs under <workflow-root>/changes/archive/)")
			}
			// tasks.md <-> plan.md correspondence (ADR 0018). This is the
			// pairing that makes a change resumable by someone who was not
			// there: resume at the first unchecked tasks.md item, read its
			// detail under the matching plan.md heading. Drift used to be
			// caught only by a prose checklist at close — which is to say,
			// after every chance to act on it had passed.
			drift, driftErr := ontostate.CheckTaskPlan(
				filepath.Join(changeDir, "tasks.md"),
				filepath.Join(changeDir, "plan.md"),
			)
			switch {
			case driftErr != nil:
				// A missing tasks.md is already reported by ValidateSkeleton
				// for the phases that require it; staying silent here avoids
				// two findings for one cause.
			case !drift.Empty():
				if len(drift.MissingFromPlan) > 0 {
					findings = append(findings, fmt.Sprintf(
						"%s: tasks.md items with no `## Task N.M` in plan.md: %v", name, drift.MissingFromPlan))
				}
				if len(drift.MissingFromTasks) > 0 {
					findings = append(findings, fmt.Sprintf(
						"%s: plan.md tasks with no item in tasks.md: %v", name, drift.MissingFromTasks))
				}
				if drift.PlanCheckboxes > 0 {
					findings = append(findings, fmt.Sprintf(
						"%s: plan.md holds %d checkbox(es); tasks.md is the single checkoff", name, drift.PlanCheckboxes))
				}
			}
			// A change that has failed verification 3+ times needs a decision, not
			// another silent retry (accept the deviation or keep fixing).
			if st.Observed.VerifyRounds >= 3 {
				findings = append(findings, fmt.Sprintf("%s: %d failed verify rounds — use fresh investigation before retrying", name, st.Observed.VerifyRounds))
			}
			// Structured evidence (ADR 0027). A missing sidecar is a note, not
			// a finding — every change created before v0.15.0 is exactly that,
			// and doctor must not fail a healthy legacy workspace.
			evFindings, evNotes := evidenceFindings(cmd, root, changeDir, name)
			findings = append(findings, evFindings...)
			for _, n := range evNotes {
				cmd.Println(n)
			}
		}
	}

	// 3. archive layout: each archive/<name> directory must hold a valid
	// onto-state.yaml marked archived:true. Stray non-directory entries are
	// ignored.
	entries, _ := filepath.Glob(filepath.Join(ontoArchiveDir(root), "*"))
	for _, entry := range entries {
		info, err := os.Stat(entry)
		if err != nil || !info.IsDir() {
			continue
		}
		name := filepath.Base(entry)
		st, err := ontostate.Load(filepath.Join(entry, "onto-state.yaml"))
		if err != nil {
			findings = append(findings, fmt.Sprintf("archive/%s: invalid or missing onto-state.yaml: %v", name, err))
			continue
		}
		if !st.Archived {
			findings = append(findings, "archive/"+name+": not marked archived: true; recover with `onto close "+st.Change+"`")
		}
		integration, tracked, integrationErr := integrationrecord.Load(entry, st.Change)
		if integrationErr != nil {
			findings = append(findings, fmt.Sprintf("archive/%s: invalid integration record: %v", name, integrationErr))
		} else if !tracked && st.IntegrationRequired {
			findings = append(findings, "archive/"+name+": required integration record is missing")
		} else if tracked {
			if err := validateIntegrationRecord(st, integration); err != nil {
				findings = append(findings, fmt.Sprintf("archive/%s: %v", name, err))
			} else if integration.Status != integrationrecord.StatusComplete {
				findings = append(findings, "archive/"+name+": integration pending; finish Git integration, then run `onto complete-integration "+st.Change+" --receipt <receipt>`")
			}
		}
	}

	// 4. version skew: the onto binary and the homonto that projected the onto
	// framework are released together and should match. When they diverge, the
	// installed skills/commands may not match this binary's behavior — tell the
	// user to re-sync. Best-effort and boundary-preserving: read only the
	// homontoVersion field from .homonto/state.json (no import of homonto's
	// projection packages); a missing file or field is silently skipped, and
	// build metadata (+dirty, etc.) is ignored so a homogeneous dev build of both
	// binaries does not report a false skew.
	if applied := workcli.HomontoAppliedVersion(root); applied != "" {
		onto := buildinfo.Resolve(Version, buildinfo.DevVersion)
		if onto != "" && workcli.NormalizeVersion(onto) != workcli.NormalizeVersion(applied) {
			findings = append(findings, fmt.Sprintf(
				"version skew: onto %s, but the onto framework was last applied by homonto %s — run `homonto update` (or align the two binaries)",
				onto, applied))
		}
	}

	// verdict
	if len(findings) == 0 {
		cmd.Println("healthy")
		return nil
	}
	for _, f := range findings {
		cmd.Println(f)
	}
	return fmt.Errorf("onto doctor: %d problem(s) found", len(findings))
}
