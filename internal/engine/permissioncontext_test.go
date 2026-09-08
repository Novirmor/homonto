package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/agentfm"
	"gopkg.in/yaml.v3"
)

func mustSubagentRenderContext(t *testing.T, e *Engine) map[string]agentfm.RenderContext {
	t.Helper()
	ctx, err := e.subagentRenderContext()
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func projectedPermissions(t *testing.T, path string) map[string]yaml.Node {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("missing host frontmatter: %s", data)
	}
	var doc struct {
		Permission map[string]yaml.Node `yaml:"permission"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Permission
}

// Match actual host request shapes; ordered YAML mappings use last-match wins.
func permissionAction(node yaml.Node, request string, inherited ...yaml.Node) string {
	action := "ask"
	for _, node := range append(inherited, node) {
		if node.Kind == yaml.ScalarNode {
			action = node.Value
			continue
		}
		for i := 0; i < len(node.Content); i += 2 {
			expr := strings.ReplaceAll(regexp.QuoteMeta(node.Content[i].Value), `\*`, ".*")
			expr = strings.ReplaceAll(expr, `\?`, ".")
			if regexp.MustCompile("(?s)^" + expr + "$").MatchString(filepath.ToSlash(request)) {
				action = node.Content[i+1].Value
			}
		}
	}
	return action
}

func TestRenamedFrameworkProtectsNativeRecordsAndGrantsOnlyInstalledSkills(t *testing.T) {
	root, realHome, base := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "record-store"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("record-store", filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home-alias")
	if err := os.Symlink(realHome, home); err != nil {
		t.Fatal(err)
	}
	content, canonicalContent := filepath.Join(base, "content"), t.TempDir()
	if err := os.Symlink(canonicalContent, content); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(canonicalContent, "skills", "chosen", "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalContent, "skills", "chosen", "SKILL.md"), []byte("---\nname: chosen\ndescription: test\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(ontoFrameworkTOML, "[frameworks.onto]", "[frameworks.delivery]", 1)
	doc = strings.Replace(doc, `scope = "project"`, `scope = "user"`, 1)
	doc += "\n[skills.chosen]\nsource = 'local:chosen'\nscope = 'user'\n"
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	for round := 0; round < 2; round++ {
		e, err := Build(context.Background(), filepath.Join(root, "homonto.toml"), home, content)
		if err != nil {
			t.Fatal(err)
		}
		current := renderFingerprint(mustSubagentRenderContext(t, e))
		if round > 0 && fingerprint != current {
			t.Fatal("installed symlinks changed permission fingerprint")
		}
		fingerprint = current
		if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
			t.Fatal(err)
		}
		for _, agent := range []string{"homonto", "onto-implementer", "onto-reviewer"} {
			permissions := projectedPermissions(t, filepath.Join(home, ".config", "opencode", "agent", agent+".md"))
			record := filepath.Join(root, "docs", "changes", "task", "tasks.md")
			if agent == "onto-reviewer" {
				if permissionAction(permissions["edit"], record) != "deny" || permissionAction(permissions["bash"], "go test") != "deny" {
					t.Fatal("read-only worker lost native tool denies")
				}
				continue
			}
			if agent == "onto-implementer" {
				if permissionAction(permissions["edit"], record) != "deny" || permissionAction(permissions["edit"], filepath.Join(root, "src", "app.go")) != "ask" {
					t.Fatal("native edits did not separate source and workflow records")
				}
				if permissionAction(permissions["edit"], filepath.Join(root, "record-store", "changes", "task", "tasks.md")) != "deny" {
					t.Fatal("native edits can reach records through their canonical path")
				}
				// OpenCode edit/write/apply_patch ask with path.relative(worktree,
				// filePath); non-Git hosts use "/" as their worktree.
				for _, base := range []string{string(filepath.Separator)} {
					for _, file := range []string{record, filepath.Join(root, "record-store", "changes", "task", "tasks.md")} {
						request, err := filepath.Rel(base, file)
						if err != nil {
							t.Fatal(err)
						}
						if permissionAction(permissions["edit"], request) != "deny" {
							t.Errorf("native relative request %q allowed from %s", request, base)
						}
					}
				}
			} else if permissionAction(permissions["edit"], record) == "deny" {
				t.Fatal("coordinator lost record editing")
			}
			for _, path := range []string{
				filepath.Join(home, ".config", "opencode", "skills", "onto", "references", "state-yaml.md"),
				filepath.Join(realHome, ".config", "opencode", "skills", "onto", "references", "state-yaml.md"),
				filepath.Join(e.CatalogRoot, "onto", "references", "state-yaml.md"),
				filepath.Join(content, "skills", "chosen", "references", "guide.md"),
				filepath.Join(canonicalContent, "skills", "chosen", "references", "guide.md"),
			} {
				if permissionAction(permissions["external_directory"], path) != "allow" {
					t.Errorf("%s cannot read installed skill %s", agent, path)
				}
			}
			for _, path := range []string{filepath.Join(home, ".ssh", "id_ed25519"), filepath.Join(realHome, ".config", "opencode", "skills", "unselected", "SKILL.md"), filepath.Join(canonicalContent, "skills", "other", "SKILL.md"), filepath.Join(root, "private", "secret")} {
				if permissionAction(permissions["external_directory"], path) != "deny" {
					t.Errorf("%s granted undeclared path %s", agent, path)
				}
			}
		}
	}
}

func TestApplyAliasAndBudgetChangesRerenderHostIdentity(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i, alias := range []string{"audit", "audit", "inspection"} {
		doc := fmt.Sprintf("[subagents.%s]\nsource = 'builtin:onto-reviewer'\n[subagents.%s.opencode]\nmodel = 'provider/model'\nvariant = '1'\nsteps = %d\n", alias, alias, 23+i)
		if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		e := buildEngine(t, home, root)
		if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, ".opencode", "agent", alias+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var header map[string]any
		if err := yaml.Unmarshal([]byte(strings.SplitN(string(data), "---\n", 3)[1]), &header); err != nil {
			t.Fatal(err)
		}
		if header["name"] != alias || header["model"] != "provider/model" || header["variant"] != "1" || header["steps"] != 23+i {
			t.Fatalf("host shape = %#v", header)
		}
		if _, err := os.Lstat(filepath.Join(root, ".opencode", "agent", "onto-reviewer.md")); !os.IsNotExist(err) {
			t.Fatalf("catalog source was also installed: %v", err)
		}
		if alias != "audit" {
			if _, err := os.Lstat(filepath.Join(root, ".opencode", "agent", "audit.md")); !os.IsNotExist(err) {
				t.Fatalf("old alias remained installed: %v", err)
			}
		}
	}
}

func TestDirectBuiltinWorkerProtectsRecordsWithoutGrantingExternalAccess(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	doc := "[subagents.worker]\nsource = 'builtin:onto-implementer'\n[subagents.worker.opencode]\nmodel = 'provider/model'\n"
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, root)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	permissions := projectedPermissions(t, filepath.Join(root, ".opencode", "agent", "worker.md"))
	if permissionAction(permissions["edit"], filepath.Join(root, "docs", "changes", "task", "tasks.md")) != "deny" {
		t.Fatal("direct worker alias can edit coordinator records")
	}
	if permissionAction(permissions["bash"], "npm run verify:pr") != "allow" {
		t.Fatal("record edit protection removed trusted routine scripts")
	}
	if _, exists := permissions["external_directory"]; exists {
		t.Fatal("direct worker gained framework external grants")
	}
}

func TestRecordEditDeniesUseResolvedRootAndProjectionHost(t *testing.T) {
	for _, placement := range []string{"nested-config", "user-scope", "non-git-control", "repo-projected"} {
		t.Run(placement, func(t *testing.T) {
			parent, home := t.TempDir(), t.TempDir()
			control, source := filepath.Join(parent, "control"), filepath.Join(parent, "source")
			for _, dir := range []string{control, source} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			host := parent
			gitRoot := parent
			if placement == "non-git-control" || placement == "repo-projected" {
				gitRoot = source
				host = string(filepath.Separator) // OpenCode's non-Git worktree.
				if placement == "repo-projected" {
					host = source
				}
			} else {
				source = parent
			}
			if out, err := exec.Command("git", "init", "--quiet", gitRoot).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			doc := fmt.Sprintf("schema_version = 2\n[workflow]\nroot = ' docs '\ngit = 'existing'\n[repos]\napp = %q\n", source)
			projection, agent := control, "onto-implementer"
			if placement == "repo-projected" {
				doc += "[subagents.worker]\nsource = 'builtin:onto-implementer'\nscope = 'project'\nrepo = 'app'\n[subagents.worker.opencode]\nmodel = 'provider/model'\n"
				projection, agent = source, "worker"
			} else {
				framework := ontoFrameworkTOML
				if placement == "user-scope" {
					framework = strings.Replace(framework, `scope = "project"`, `scope = "user"`, 1)
				}
				doc += framework
			}
			if err := os.WriteFile(filepath.Join(control, "homonto.toml"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			e := buildEngine(t, home, control)
			records := filepath.Join(control, "docs")
			if e.Cfg.Workflow.Root != " docs " || e.WorkspaceLayout.WorkflowRoot != records {
				t.Fatalf("fixture must distinguish raw and resolved root: %q -> %q", e.Cfg.Workflow.Root, e.WorkspaceLayout.WorkflowRoot)
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(projection, ".opencode", "agent", agent+".md")
			if placement == "user-scope" {
				path = filepath.Join(home, ".config", "opencode", "agent", agent+".md")
			}
			permissions := projectedPermissions(t, path)
			request := func(file string) string {
				t.Helper()
				rel, err := filepath.Rel(host, file)
				if err != nil {
					t.Fatal(err)
				}
				return rel
			}
			// Model the host's inherited rules followed by the agent's rules.
			// Source command cwd never changes edit's host-worktree-relative base.
			for _, defaultAction := range []string{"allow", "ask", "deny"} {
				var inherited yaml.Node
				if err := yaml.Unmarshal([]byte(fmt.Sprintf("\"*\": %s\n\"*src/ask/*\": ask\n\"*src/deny/*\": deny\n", defaultAction)), &inherited); err != nil {
					t.Fatal(err)
				}
				for _, tc := range []struct{ file, want string }{
					{filepath.Join(records, "changes", "task", "tasks.md"), "deny"},
					{filepath.Join(source, "docs", "api.md"), defaultAction},
					{filepath.Join(source, "src", "app.go"), defaultAction},
					{filepath.Join(source, "src", "ask", "app.go"), "ask"},
					{filepath.Join(source, "src", "deny", "secret.go"), "deny"},
				} {
					if got := permissionAction(permissions["edit"], request(tc.file), *inherited.Content[0]); got != tc.want {
						t.Errorf("host=%s inherited=%s edit(%q) = %s, want %s", host, defaultAction, request(tc.file), got, tc.want)
					}
				}
			}
			if permissionAction(permissions["edit"], filepath.Join(records, "tasks", "task", "notes.md")) != "deny" {
				t.Fatal("absolute resolved records path lost protection")
			}
			if placement != "repo-projected" {
				if permissionAction(permissions["external_directory"], filepath.Join(records, "tasks", "task", "notes.md")) != "deny" {
					t.Fatal("external resolved records path lost protection")
				}
				coordinator := projectedPermissions(t, strings.Replace(path, agent+".md", "homonto.md", 1))
				if permissionAction(coordinator["external_directory"], filepath.Join(records, "tasks", "task", "notes.md")) != "allow" {
					t.Fatal("coordinator lost resolved records access")
				}
				if _, exists := coordinator["edit"]; exists {
					t.Fatal("coordinator edit permissions must remain inherited")
				}
			}
		})
	}
}

func TestPermissionHostProbeDoesNotGuessAfterFailure(t *testing.T) {
	for _, name := range []string{"missing", "not a git repository"} {
		if _, err := permissionHostWorktree(filepath.Join(t.TempDir(), name)); err == nil {
			t.Fatal("failed Git probe must not silently select a guessed host base")
		}
	}
}

func TestNativeFrameworkNameMismatchDoesNotPublish(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	fw := filepath.Join(root, "framework")
	if err := os.MkdirAll(filepath.Join(fw, "skills", "custom"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(fw, "framework.toml"), "name = 'custom'\nversion = '1.0.0'\n[skills]\ncustom = 'skills/custom'\n[subagents]\nnav = 'native.md'\n")
	write(filepath.Join(fw, "skills", "custom", "SKILL.md"), "original skill\n")
	write(filepath.Join(fw, "native.md"), "---\nname: nav\npermission:\n  edit: deny\n---\noriginal prompt\n")
	write(filepath.Join(root, "homonto.toml"), fmt.Sprintf("[frameworks.custom]\nsource = %q\nscope = 'project'\n[subagents.nav.opencode]\nmodel = 'provider/model'\n", "local:"+fw))
	e := buildEngine(t, home, root)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(root, ".opencode", "agent", "nav.md")
	before, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	bad := "---\r\nname: wrong-host-name\r\npermission:\r\n  edit: deny\r\n---\nauthor's edited prompt\n"
	write(filepath.Join(fw, "native.md"), bad)
	write(filepath.Join(fw, "skills", "custom", "SKILL.md"), "unpublished skill edit\n")
	e = buildEngine(t, home, root)
	if err := e.Apply(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "frontmatter name") || !strings.Contains(err.Error(), "nav") {
		t.Fatalf("native name mismatch = %v", err)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != string(before) {
		t.Fatalf("failed validation changed installed agent: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(e.CatalogRoot, "custom", "SKILL.md")); err != nil || string(got) != "original skill\n" {
		t.Fatalf("failed validation published skill: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(fw, "native.md")); err != nil || string(got) != bad {
		t.Fatalf("failed validation mutated author source: %q, %v", got, err)
	}
}
