package ontocli

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// scaleThresholdFiles / scaleThresholdLines are the size gates above which a
// change verifies at "full" scale. The file gate mirrors the tweak preset's ≤5
// non-test-file limit, so a change larger than a tweak gets full verification.
const (
	scaleThresholdFiles = 5
	scaleThresholdLines = 200
)

// diffScale measures the change's diff against its base ref and returns the
// non-test file count, total changed lines, and the derived level. It shells
// out to git; a non-repo/parse failure is surfaced by the caller.
//
// A missing base ref is an ERROR, not a fallback. The old fallback measured
// the worktree against HEAD — but the workflow commits per task, so at verify
// time the tree is clean and a 1000-line change scored {files:0, lines:0} →
// "light", silently selecting the weakest verification gate on the default
// path (nothing else requires base-ref to be recorded).
func diffScale(root, baseRef string) (files, lines int, level string, err error) {
	if strings.TrimSpace(baseRef) == "" {
		return 0, 0, "", fmt.Errorf("onto scale: no base ref recorded for this change — the committed diff cannot be measured without one; run `onto set base-ref <change> <ref>` first")
	}
	args := []string{"-C", root, "diff", "--numstat", baseRef + "..HEAD"}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return 0, 0, "", fmt.Errorf("onto scale: git diff failed (is %s a git repo, and is %q a valid ref?): %w", root, baseRef, err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		cols := strings.Fields(sc.Text())
		if len(cols) < 3 {
			continue
		}
		path := cols[2]
		// A binary file shows "-" for add/del; count it as one changed file, no lines.
		add, _ := strconv.Atoi(cols[0])
		del, _ := strconv.Atoi(cols[1])
		lines += add + del
		if !isTestPath(path) {
			files++
		}
	}
	level = "light"
	if files > scaleThresholdFiles || lines > scaleThresholdLines {
		level = "full"
	}
	return files, lines, level, nil
}

// isTestPath reports whether a changed path is a test file (excluded from the
// file-count gate, mirroring the preset limits that never count tests).
func isTestPath(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") {
		return true
	}
	// Any path segment named "test" or "tests" marks a test tree, whether the
	// path is rooted (test/…) or nested (internal/x/tests/…).
	for _, seg := range strings.Split(path, "/") {
		if seg == "test" || seg == "tests" {
			return true
		}
	}
	return false
}

// scaleCmd builds "onto scale <change> [--set]": measure the change's diff and
// derive the verification level (light|full) from it — a measured fact rather
// than a judgment call (B1). --set records it via verify-scale.
func scaleCmd() *cobra.Command {
	var (
		dir    string
		doSet  bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "scale <change>",
		Short: "Derive the verification level from the change's measured diff",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			if doSet {
				unlock, err := lockOnto(dir)
				if err != nil {
					return err
				}
				defer unlock()
			}
			changeDir := filepath.Join(changesDir(dir), name)
			st, err := ontostate.LoadChange(changeDir)
			if err != nil {
				return err
			}
			files, lines, level, err := diffScale(dir, st.BaseRef)
			if err != nil {
				return err
			}
			if doSet {
				st.Verify.Scale = level
				if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
					return err
				}
			}
			if asJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "{\"files\":%d,\"lines\":%d,\"level\":%q,\"recorded\":%t}\n", files, lines, level, doSet)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %d non-test file(s), %d changed line(s) → verify-scale %s%s\n",
				name, files, lines, level, map[bool]string{true: " (recorded)", false: ""}[doSet])
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().BoolVar(&doSet, "set", false, "record the derived level via verify-scale")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the measurement as JSON")
	return cmd
}
