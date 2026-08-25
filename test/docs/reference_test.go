// Package docs checks that the documentation still describes the product.
//
// Docs rot silently. A command renamed, a flag removed, a guide left
// pointing at a file that no longer exists — none of those break a build,
// and all of them mislead the next person. These tests fail instead.
package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/cli"
	"github.com/spf13/cobra"
)

// repoRoot is the repository root, relative to this test's directory.
const repoRoot = "../.."

// read loads a repository file.
func read(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

// commandPaths collects every command a user can type.
func commandPaths() []string {
	var out []string
	var walk func(cmd *cobra.Command, prefix string)
	walk = func(cmd *cobra.Command, prefix string) {
		for _, child := range cmd.Commands() {
			name := child.Name()
			if prefix != "" {
				name = prefix + " " + name
			}
			out = append(out, name)
			walk(child, name)
		}
	}
	walk(cli.NewRootCmd(), "")
	sort.Strings(out)
	return out
}

// TestCLIReferenceDocumentsEveryCommand proves the reference is complete
// rather than representative. It is the one document that claims to list
// everything, so it is the one that must.
func TestCLIReferenceDocumentsEveryCommand(t *testing.T) {
	reference := read(t, "docs/guides/cli-reference.md")
	for _, path := range commandPaths() {
		if !strings.Contains(reference, "homonto "+path) {
			t.Errorf("the CLI reference does not document `homonto %s`", path)
		}
	}
}

// TestNoGuideMentionsARemovedCommand proves the docs describe THIS product
// and not the one it replaced.
func TestNoGuideMentionsARemovedCommand(t *testing.T) {
	removed := []string{
		"homonto plan", "homonto apply", "homonto import", "homonto cache",
		"homonto.toml", "`onto`", "`to`",
	}
	for _, rel := range docFiles(t) {
		body := read(t, rel)
		for _, phrase := range removed {
			if !strings.Contains(body, phrase) {
				continue
			}
			// Three documents exist to explain that the old product is
			// gone, and cannot do that without naming it: the decision
			// records, the release notes, and the redesign spec the
			// rewrite was built from.
			if strings.HasPrefix(rel, "docs/adr/") ||
				strings.Contains(rel, "release-notes") ||
				strings.Contains(rel, "workflow-redesign") {
				continue
			}
			t.Errorf("%s mentions %q, which this product does not have", rel, phrase)
		}
	}
}

// TestEveryDocumentedWorkflowExists guards the two workflow names.
func TestEveryDocumentedWorkflowExists(t *testing.T) {
	readme := read(t, "README.md")
	for _, want := range []string{
		"plan → do → done",
		"open → design → build → verify → close",
		"Claude Code", "OpenCode", "Linux and macOS",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README does not describe %q", want)
		}
	}
}

// linkPattern matches a markdown link to a repository-relative path.
var linkPattern = regexp.MustCompile(`\]\(([^)#:]+?)(?:#[^)]*)?\)`)

// TestEveryInternalLinkResolves proves no guide points at a file the
// rewrite deleted.
func TestEveryInternalLinkResolves(t *testing.T) {
	for _, rel := range docFiles(t) {
		body := read(t, rel)
		dir := filepath.Dir(rel)
		for _, match := range linkPattern.FindAllStringSubmatch(body, -1) {
			target := match[1]
			if strings.HasPrefix(target, "http") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			resolved := filepath.Join(repoRoot, filepath.FromSlash(filepath.Join(dir, target)))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to %q, which does not exist", rel, target)
			}
		}
	}
}

// TestArtifactPathsAreDocumentedCorrectly pins where records live. Getting
// this wrong in a guide sends someone looking in a directory that has not
// existed since the layout was corrected.
func TestArtifactPathsAreDocumentedCorrectly(t *testing.T) {
	getting := read(t, "docs/guides/getting-started.md")
	if !strings.Contains(getting, "docs/homonto/tasks/archive/") {
		t.Error("getting started does not say where an archived task lands")
	}
	config := read(t, "docs/guides/configuration.md")
	if !strings.Contains(config, ".homonto/config.toml") {
		t.Error("the configuration guide does not name the manifest")
	}
	for _, rel := range docFiles(t) {
		body := read(t, rel)
		if strings.Contains(body, "active/") && !strings.Contains(body, "docs/homonto") {
			t.Errorf("%s uses the old active/ layout", rel)
		}
	}
}

// docFiles lists every markdown file that documents the current product.
func docFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	roots := []string{"README.md", "AGENTS.md", "docs"}
	for _, root := range roots {
		abs := filepath.Join(repoRoot, root)
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			out = append(out, root)
			continue
		}
		err = filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			rel, relErr := filepath.Rel(filepath.Join(repoRoot), path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			// Scratch directories are gitignored working notes, not
			// documentation.
			if strings.HasPrefix(rel, "docs/superpowers/") {
				return nil
			}
			out = append(out, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(out)
	return out
}
