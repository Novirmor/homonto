package catalog

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

// These are prompt contracts; executable lifecycle coverage lives separately in
// workflow_sequence_test.go and does not pretend to verify GitHub publication.
func TestIntegrationPromptReceiptExamples(t *testing.T) {
	commands := regexp.MustCompile("onto complete-integration[^`\n]+")
	prExamples := 0
	err := fs.WalkDir(embedded.FS, "skills", func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(file, ".md") {
			return err
		}
		data, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			return err
		}
		for _, command := range commands.FindAllString(string(data), -1) {
			if strings.Contains(command, "pr:<") {
				prExamples++
				for _, flag := range []string{"--head <observed-headOID>", "--repo <alias>", "--dir \"<configRoot>\""} {
					if !strings.Contains(command, flag) {
						t.Errorf("%s PR completion missing %s: %s", file, flag, command)
					}
				}
			}
			if (strings.Contains(command, "unchanged:<") || strings.Contains(command, "merge:<")) && strings.Contains(command, "--head") {
				t.Errorf("%s uses PR-only --head for a local receipt: %s", file, command)
			}
		}
		return nil
	})
	if err != nil || prExamples < 5 {
		t.Fatalf("receipt inventory: examples=%d err=%v", prExamples, err)
	}
}

func TestIntegrationPromptRecoveryContracts(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []string
	}{
		{"skills/homonto/references/publication.md", []string{
			"## Per-repo no-op first", "in either recorded integration mode", "external claim",
			"independently observing the delivered PR head on that canonical remote",
			"local HEAD or the desired delivery SHA", "Before archive", "identity-checked terminal receiver API",
			"--state-id <recorded-state-id>", "cannot rebind an existing worktree",
			"no closing directive", "For every repository and every attempt", "body hash",
			"Do not copy an origin repo's rendered body into another repo", "leave it immutable",
			"independently of whether local PR HEAD is ahead", "before local integration",
		}},
		{"skills/onto-close/SKILL.md", []string{
			"before archive, recording its identity and absolute path", "reuse the preallocated receiver's recorded path",
			"Route per-repo no-op before merge/PR", "unchanged:<receivingSHA>",
			"--body-file \"$REPO_BODY_FILE\"", "no closing directive", "For every repo and retry regenerate",
			"Every source has a proven `unchanged:` receipt", "does not verify remote publication",
		}},
		{"skills/onto-close/references/ship-handoff.md", []string{
			"Archived `ship.md` contains no closing directive", "For every destination and every retry",
			"sanitize that text only in the temporary rendering", "never mutate history",
			"destination canonical ID/host, target, observed headOID, body path and body hash",
		}},
		{"skills/h-continue-pr/SKILL.md", []string{
			"archives independently of whether local PR HEAD is ahead of upstream",
			"canonical PR host/repository ID/number plus source alias", "before local integration",
			"every freshly collected feedback item", "Resume the first missing integration/verification/delivery step",
			"skip `to new`, planning/build, and `to done`", "read the archive directly",
		}},
		{"skills/to-done/SKILL.md", []string{
			"Before archival", "preallocating", "reuse the receiver path recorded before archive",
			"already archived recovery without a receiver", "local PR HEAD is not ahead of upstream",
		}},
		{"skills/onto/references/state-yaml.md", []string{"unchanged:<receivingSHA>", "external claim", "--head <observed-headOID>", "tree-identical"}},
		{"skills/to/SKILL.md", []string{"After conversion", "preserve Owner, Repo, Cwd, Files, Change and Verify", "routes to `to-plan`", "do not fake a phase rewind"}},
		{"skills/to-plan/SKILL.md", []string{"converted do", "skip `to phase`", "carry Owner/Repo/Cwd", "Do not drop ownership/root fields"}},
		{"skills/to-do/SKILL.md", []string{"Converted tasks must carry Owner/Repo/Cwd", "route to `to-plan` for repair", "without changing recorded do"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			text := hPromptText(t, tc.file)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, obsolete := range []string{"--body-file <archive>/ship.md", "Use this body directly with the repository's PR command"} {
				if strings.Contains(text, obsolete) {
					t.Errorf("unsafe cached publication: %q", obsolete)
				}
			}
		})
	}
}
