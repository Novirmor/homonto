package tocli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// ErrQuietFindings is what `to doctor --quiet` returns when there are findings:
// the caller (cmd/to/main.go) must exit non-zero WITHOUT printing. Aliased to
// the shared workcli sentinel so the quiet contract and the errors.Is check in
// cmd/to/main.go hold for both workflow CLIs from one definition.
var ErrQuietFindings = workcli.ErrQuietFindings

// doctorCmd builds "to doctor": a strictly read-only, config-independent
// workspace-health diagnostic. It is NOT gated on the framework install — a
// broken workspace is a finding, not a refusal. --quiet prints nothing and
// signals via exit code only: the hook primitive (see the enforcement guide).
func doctorCmd() *cobra.Command {
	var (
		dir   string
		quiet bool
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report to workspace health (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			findings, err := collectFindings(dir)
			if err != nil {
				return err
			}
			if quiet {
				if len(findings) > 0 {
					return ErrQuietFindings
				}
				return nil
			}
			if len(findings) == 0 {
				cmd.Println("healthy")
				return nil
			}
			for _, f := range findings {
				cmd.Println(f)
			}
			return fmt.Errorf("to doctor: %d problem(s) found", len(findings))
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing; signal findings via exit code only")
	return cmd
}

// siblingDuplicates lists active change names that also exist as active
// directories in the sibling workflow's tree.
func siblingDuplicates(root, siblingDir string) ([]string, error) {
	mine, err := activeNames(tasksDir(root))
	if err != nil {
		return nil, err
	}
	wf, err := workcli.WorkflowRoot(root)
	if err != nil {
		return nil, err
	}
	theirs, err := activeNames(filepath.Join(wf, siblingDir))
	if err != nil {
		return nil, err
	}
	dupes := []string{}
	for name := range mine {
		if theirs[name] {
			dupes = append(dupes, name)
		}
	}
	sort.Strings(dupes)
	return dupes, nil
}

// activeNames lists real directory names under dir, skipping the archive.
func activeNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" {
			continue
		}
		names[e.Name()] = true
	}
	return names, nil
}

// collectFindings walks the to workspace. A missing docs/tasks/ is healthy
// (the repo may not use to yet), matching status's behavior.
func collectFindings(root string) ([]string, error) {
	findings := []string{}

	// 1. Active changes: state validity, wedged terminal state, the plan.md
	// artifact, and the lightweight task contract the skills depend on.
	dirents, err := os.ReadDir(tasksDir(root))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("to doctor: reading %s: %w", tasksDir(root), err)
	}
	for _, d := range dirents {
		if !d.IsDir() || d.Name() == "archive" {
			continue
		}
		name := d.Name()
		st, err := tostate.Load(statePath(root, name))
		if err == nil {
			err = st.Validate()
		}
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: invalid or missing %s: %v", name, tostate.FileName, err))
			continue
		}
		if st.Terminal() {
			verb := "to abandon " + name
			if st.Phase == tostate.PhaseDone {
				verb = "to done " + name + " --verified"
			}
			findings = append(findings, fmt.Sprintf("%s: terminal (%s) but still active — an interrupted archive; re-run `%s` to complete it", name, st.Phase, verb))
			continue
		}
		if len(st.Repos) > 0 {
			if _, err := worktreeDirty(root); err != nil {
				findings = append(findings, fmt.Sprintf("%s: config repo is not a usable git worktree", name))
			} else if names, dirs, err := scopeDirs(root, st.Repos); err != nil {
				findings = append(findings, fmt.Sprintf("%s: cross-repo scope unavailable: %v", name, err))
			} else {
				for _, repo := range names {
					if _, err := worktreeDirty(dirs[repo]); err != nil {
						findings = append(findings, fmt.Sprintf("%s: declared repo %s is not a usable git worktree", name, repo))
					}
				}
			}
		}
		plan, err := os.ReadFile(planPath(root, name))
		if err != nil {
			findings = append(findings, name+": plan.md is missing")
			continue
		}
		if st.Phase == tostate.PhaseDo {
			for _, finding := range planContractFindings(string(plan)) {
				findings = append(findings, name+": "+finding)
			}
		}
	}

	// 2. Global-name uniqueness (ADR 0042): an active name colliding with
	// the onto workflow's active tree is ambiguous for agent routing.
	if dupes, err := siblingDuplicates(root, "changes"); err == nil {
		for _, name := range dupes {
			findings = append(findings, fmt.Sprintf("%s: also active in the onto workflow (<workflow-root>/changes/%s) — active names are globally unique; convert or rename one", name, name))
		}
	}

	// 3. Archive entries: must hold a valid, terminal state.
	archents, err := os.ReadDir(archiveDir(root))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("to doctor: reading %s: %w", archiveDir(root), err)
	}
	for _, d := range archents {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		st, err := tostate.Load(filepath.Join(archiveDir(root), name, tostate.FileName))
		if err == nil {
			err = st.Validate()
		}
		if err != nil {
			findings = append(findings, fmt.Sprintf("archive/%s: invalid or missing %s: %v", name, tostate.FileName, err))
			continue
		}
		if !st.Terminal() {
			findings = append(findings, fmt.Sprintf("archive/%s: archived but not terminal (phase %s)", name, st.Phase))
		}
	}

	// 3. Version skew: to and the homonto that applied the to framework are
	// released together and should match. Best-effort and boundary-preserving:
	// read only homontoVersion from .homonto/state.json; missing file or field
	// is silently skipped, and build metadata is ignored so a homogeneous dev
	// build of both binaries does not report a false skew.
	if applied := workcli.HomontoAppliedVersion(root); applied != "" {
		if me := buildinfo.Resolve(Version, buildinfo.DevVersion); me != "" && workcli.NormalizeVersion(me) != workcli.NormalizeVersion(applied) {
			findings = append(findings, fmt.Sprintf(
				"version skew: to %s, but the to framework was last applied by homonto %s — run `homonto update` (or align the two binaries)",
				me, applied))
		}
	}

	return findings, nil
}
