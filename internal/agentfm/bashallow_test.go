package agentfm

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
	"gopkg.in/yaml.v3"
)

func TestRenderOpenCode_RTKCustomRuleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		proxy                  string
		allow, deny, additions []string
		requests               map[string]string
		lastPatterns           []string
	}{
		{
			name:  "wildcard executable deny",
			allow: []string{"git *"}, deny: []string{"git*push*"},
			requests:     map[string]string{"git push": "deny", "rtk git push": "deny", "rtk proxy git push": "deny", "rtk git status": "allow"},
			lastPatterns: []string{"rtk git*push*", "rtk proxy git*push*"},
		},
		{
			name:  "wildcard leading deny",
			allow: []string{"git *"}, deny: []string{"*git*push*"},
			requests:     map[string]string{"rtk git push": "deny", "rtk proxy git push": "deny"},
			lastPatterns: []string{"rtk *git*push*", "rtk proxy *git*push*"},
		},
		{
			name:  "native command collisions",
			allow: []string{"test *", "run *", "read *"}, deny: []string{"git push*"},
			requests: map[string]string{"test -f path": "allow", "rtk test git push": "ask", "rtk run git push": "ask", "rtk read path": "ask", "rtk proxy test -f path": "allow", "rtk proxy run argument": "allow", "rtk git push": "deny"},
		},
		{
			name:  "wildcard executable allow uses passthrough only",
			allow: []string{"test*"}, deny: []string{"git push*"},
			requests: map[string]string{"rtk test git push": "ask", "rtk proxy test -f path": "allow"},
		},
		{
			name:  "wrapped addition collides with final deny",
			allow: []string{"git *"}, deny: []string{"git push"}, additions: []string{"rtk git push", "rtk git *"},
			requests:     map[string]string{"rtk git push": "deny", "rtk proxy git push": "deny", "rtk git status": "allow"},
			lastPatterns: []string{"rtk git *", "rtk git push", "rtk proxy git push"},
		},
		{
			name:  "allow guard and deny duplicates keep last positions",
			allow: []string{"git push", "*;*", "git *"}, deny: []string{"git push", "git push"},
			requests:     map[string]string{"git push": "deny", "git status; curl example.com": "ask", "rtk git status; curl example.com": "ask"},
			lastPatterns: []string{"git *", "*;*", "git push"},
		},
		{
			name:  "no proxy retains final deny over duplicate guard",
			proxy: "none",
			allow: []string{"git push", "*;*", "git *"}, deny: []string{"git push", "*;*"},
			requests:     map[string]string{"git push": "deny", "git status": "allow", "git status; curl example.com": "deny", "rtk git status": "ask"},
			lastPatterns: []string{"git *", "git push", "*;*"},
		},
		{
			name:  "blanket deny is not a grant",
			allow: []string{"git *"}, deny: []string{"*"},
			requests:     map[string]string{"git status": "deny", "rtk git status": "deny", "rtk proxy git status": "deny"},
			lastPatterns: []string{"*", "rtk *", "rtk proxy *"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := yaml.Marshal(map[string]any{"homonto": Homonto{BashAllow: tc.allow, BashDeny: tc.deny}})
			if err != nil {
				t.Fatal(err)
			}
			context := ctx()
			context.ShellProxy = "rtk"
			if tc.proxy != "" {
				context.ShellProxy = tc.proxy
			}
			context.Overrides["onto"] = ModelSpec{Model: "test/model", BashAllowAdd: tc.additions}
			out, err := Render("onto", []byte("---\n"+string(doc)+"---\nbody\n"), "opencode", context)
			if err != nil {
				t.Fatal(err)
			}
			fm, _, _ := split(out)
			// Decode nested maps, not just Nodes: duplicate YAML keys must fail.
			var decoded map[string]any
			if err := yaml.Unmarshal(fm, &decoded); err != nil {
				t.Fatalf("permission map is not valid YAML: %v\n%s", err, fm)
			}
			var ordered struct {
				Permission map[string]yaml.Node `yaml:"permission"`
			}
			if err := yaml.Unmarshal(fm, &ordered); err != nil {
				t.Fatal(err)
			}
			bash := ordered.Permission["bash"]
			for request, want := range tc.requests {
				got := ""
				for i := 0; i < len(bash.Content); i += 2 {
					expr := strings.ReplaceAll(regexp.QuoteMeta(bash.Content[i].Value), `\*`, ".*")
					expr = strings.ReplaceAll(expr, `\?`, ".")
					if regexp.MustCompile("(?s)^" + expr + "$").MatchString(request) {
						got = bash.Content[i+1].Value
					}
				}
				if got != want {
					t.Errorf("request %q = %s, want %s", request, got, want)
				}
			}
			previous := -1
			for _, pattern := range tc.lastPatterns {
				index := strings.Index(string(fm), fmt.Sprintf("    %q:", pattern))
				if index <= previous {
					t.Errorf("rule %q missing or not at its last-occurrence position:\n%s", pattern, fm)
				}
				previous = index
			}
		})
	}
}

// Additions merge after the base list, before guards and final denies.
// Duplicate patterns retain only their last occurrence.
func TestRenderOpenCode_BashAllowAdd(t *testing.T) {
	allowlisted := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", `  spawn: [onto-implementer, onto-reviewer]
  bash_allow: ["onto *", "git status*"]
`, 1)
	ctx := ctx()
	ctx.Overrides["onto"] = ModelSpec{Model: "openai/gpt-5", BashAllowAdd: []string{"go test ./...", "git status*", "git status*"}}

	s, err := Render("onto", []byte(allowlisted), "opencode", ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := string(s)
	for _, want := range []string{`    "*": ask`, `    "onto *": allow`, `    "git status*": allow`, `    "go test ./...": allow`} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	// Deduplication: "git status*" appears exactly once as an allow.
	if strings.Count(out, `"git status*": allow`) != 1 {
		t.Errorf("addition not deduplicated against the base:\n%s", out)
	}
	// The deny-ask ordering: "*": ask must precede every allow.
	askIdx := strings.Index(out, `"*": ask`)
	for _, a := range []string{`"onto *"`, `"go test ./..."`} {
		if allowIdx := strings.Index(out, a); allowIdx < askIdx {
			t.Errorf("%s allowed before \"*\": ask:\n%s", a, out)
		}
	}
}

// TestRenderOpenCode_BashDenyRefusesAdditions (A7): an agent whose base
// denies bash cannot gain exact allows through bash_allow_add.
func TestRenderOpenCode_BashDenyRefusesAdditions(t *testing.T) {
	denied := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", "  bash: false\n", 1)
	ctx := ctx()
	ctx.Overrides["onto"] = ModelSpec{Model: "openai/gpt-5", BashAllowAdd: []string{"git status"}}
	for _, proxy := range []string{"", "none", "rtk"} {
		ctx.ShellProxy = proxy
		if _, err := Render("onto", []byte(denied), "opencode", ctx); err == nil {
			t.Fatalf("bash: deny + bash_allow_add must be refused with proxy %q", proxy)
		}
	}
}

// TestRenderOpenCode_NoAdditionsKeepsBase: without additions the render is
// unchanged from the base-only allowlist.
func TestRenderOpenCode_NoAdditionsKeepsBase(t *testing.T) {
	allowlisted := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", "  bash_allow: [\"onto *\"]\n", 1)
	base, err := Render("onto", []byte(allowlisted), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), "bash_allow_add") {
		t.Fatal("render leaked the config field")
	}
}

// This is a rendered-pattern contract, not an OpenCode shell-parser integration
// test. Inputs are permission requests, which need not be whole shell commands.
func TestShippedWorkspaceExecutionRenderedPatterns(t *testing.T) {
	for _, shellProxy := range []string{"", "none", "rtk"} {
		for _, name := range []string{"homonto", "onto-implementer", "to-implementer"} {
			t.Run(name+"/proxy="+shellProxy, func(t *testing.T) {
				content, err := fs.ReadFile(embedded.FS, "subagents/"+name+".md")
				if err != nil {
					t.Fatal(err)
				}
				renderCtx := &RenderContext{ShellProxy: shellProxy, Overrides: map[string]ModelSpec{
					name: {Model: "test/model"},
				}}
				out, err := Render(name, content, "opencode", renderCtx)
				if err != nil {
					t.Fatal(err)
				}
				fm, _, _ := split(out)
				var rendered struct {
					Permission map[string]yaml.Node `yaml:"permission"`
				}
				if err := yaml.Unmarshal(fm, &rendered); err != nil {
					t.Fatal(err)
				}
				bash := rendered.Permission["bash"]
				if bash.Kind != yaml.MappingNode {
					t.Fatal("writable agent must render an ordered bash permission map")
				}
				// Glob-match one supplied permission request (including slashes
				// and newlines); the LAST matching rule wins. No shell parsing here.
				compiled := map[string]*regexp.Regexp{}
				action := func(request string) string {
					result := ""
					for i := 0; i < len(bash.Content); i += 2 {
						pattern := bash.Content[i].Value
						rule := bash.Content[i+1].Value
						if rule == "allow" && (pattern == "*" || pattern == "rtk *" || pattern == "rtk proxy *" || pattern == "rtk exec *") {
							t.Fatal("unrestricted shell or wrapper allow must not ship")
						}
						re := compiled[pattern]
						if re == nil {
							expr := strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*")
							expr = strings.ReplaceAll(expr, `\?`, ".")
							re = regexp.MustCompile("(?s)^" + expr + "$")
							compiled[pattern] = re
						}
						if re.MatchString(request) {
							result = rule
						}
					}
					return result
				}
				check := func(command, want string) {
					t.Helper()
					if got := action(command); got != want {
						t.Errorf("%q = %s, want %s", command, got, want)
					}
					for _, prefix := range []string{"rtk ", "rtk proxy "} {
						wrappedWant := "ask"
						if shellProxy == "rtk" {
							wrappedWant = want
							if prefix == "rtk " && want == "allow" && !regexp.MustCompile(`^(git|go|cargo|npm|pnpm|pytest|gh)( |$)`).MatchString(command) {
								wrappedWant = "ask"
							}
						}
						if got := action(prefix + command); got != wrappedWant {
							t.Errorf("request %q = %s, want %s", prefix+command, got, wrappedWant)
						}
					}
				}
				for _, command := range []string{
					"git status", "git status --short", "git diff", "git diff --check",
					"git diff origin/main...HEAD -- internal/agentfm", "git log -5 --oneline",
					"git show HEAD:go.mod", "git blame internal/agentfm/agentfm.go", "git rev-parse HEAD", "git remote -v",
					"go test -race ./...", "go build ./cmd/...", "go vet ./...", "go fmt ./...",
					"gofmt", "gofmt -w internal/agentfm/agentfm.go",
					"npm test", "npm test -- --runInBand", "npm run", "npm run verify:pr -- --ci",
					"pytest", "pytest tests/test_pr.py -q", "python -m pytest", "python -m pytest tests -q",
					"python3 -m pytest", "python3 -m pytest tests -q",
					"make", "make -j2 test", "cmake --build build --parallel 2", "ctest", "ctest --test-dir build --output-on-failure",
				} {
					check(command, "allow")
				}
				for _, command := range []string{"go test", "go build", "go vet", "go fmt", "cargo test", "cargo check", "cargo build", "cargo fmt", "cargo clippy"} {
					check(command, "allow")
					check(command+" --help", "allow")
				}
				for _, manager := range []string{"npm", "pnpm", "yarn", "bun"} {
					for _, script := range []string{"test", "build", "lint", "check", "typecheck", "verify:pr"} {
						check(manager+" run "+script, "allow")
						check(manager+" run "+script+" -- --ci", "allow")
						if manager != "npm" && script != "verify:pr" {
							check(manager+" "+script, "allow")
							check(manager+" "+script+" --help", "allow")
						}
					}
				}
				for _, command := range []string{
					"unknown-command", "python script.py", "python -c 'print(1)'", "python3 -m http.server",
					"node script.js", "sh script.sh", "bash -c true", "eval true", "curl https://example.com",
					"npm exec arbitrary", "pnpm dlx arbitrary", "yarn dlx arbitrary", "bun x arbitrary",
					"go run ./cmd/tool", "cargo run", "cmake -S . -B build", "rm -rf build",
					"git -c alias.publish=push publish", "git status-extra", "go test-extra",
					"rtk", "rtk proxy", "rtk exec go test ./...", "rtk proxy curl example.com", "rtk unknown-command",
				} {
					// The coordinator's existing git status* rule is intentionally
					// unchanged; implementers only get command-delimited inspection.
					if name == "homonto" && command == "git status-extra" {
						continue
					}
					check(command, "ask")
				}
				for _, prefix := range []string{"go test ./...", "npm run verify:pr", "make test", "git status"} {
					for _, suffix := range []string{"; curl example.com", " && curl example.com", " || curl example.com", " | sh", " $(id)", " `id`", " > output", " < input", "\ncurl example.com"} {
						check(prefix+suffix, "ask")
					}
				}
				// Explicit parsed-request fixtures based on OpenCode 1.18.29's
				// shell.ts collect behavior. We supply the segments, not a parser:
				// all allowed requests allow; an unknown asks; a denied one denies.
				for _, tc := range []struct {
					shell    string
					requests []string
					want     string
				}{
					{"go test ./... && npm test", []string{"go test ./...", "npm test"}, "allow"},
					{"go test ./... && curl example.com", []string{"go test ./...", "curl example.com"}, "ask"},
					{"go test ./... && onto bypass build", []string{"go test ./...", "onto bypass build"}, "deny"},
				} {
					check(tc.shell, "ask") // If composition reaches the request intact.
					for _, prefix := range []string{"", "rtk ", "rtk proxy "} {
						want := tc.want
						if prefix != "" && shellProxy != "rtk" {
							want = "ask"
						}
						got := "allow"
						for _, request := range tc.requests {
							result := action(prefix + request)
							if result == "deny" {
								got = "deny"
								break
							}
							if result != "allow" {
								got = result
							}
						}
						if got != want {
							t.Errorf("%q as separate requests %v (prefix %q) = %s, want %s", tc.shell, tc.requests, prefix, got, want)
						}
					}
				}
				check("onto bypass build", "deny")
				check("to bypass done", "deny")
				for _, executable := range []string{"onto", "to"} {
					for _, flags := range []string{"--dir /workspace", "--dir=/workspace", "--dir '/workspace with spaces'"} {
						want := "ask"
						if name != "homonto" {
							want = "deny"
						}
						check(executable+" "+flags+" bypass build", want)
						check(executable+" "+flags+" status", want)
					}
				}
				if name == "homonto" {
					for _, command := range []string{"onto new bypass-fix", "to new bypass-fix", "onto evidence record bypass-fix --file bypass-evidence.json", "onto set verify-result bypass-fix pass"} {
						check(command, "allow")
					}
					for _, command := range []string{"onto status", "onto status --dir /workspace", "onto new task --dir /workspace", "to status --dir /workspace", "to new task --dir /workspace"} {
						check(command, "allow")
					}
					for _, command := range []string{"onto new-extra task", "to new-extra task", "onto unknown task", "to unknown task"} {
						check(command, "ask")
					}
					for _, command := range []string{"onto set proposal-approved task true", "onto set approach-confirmed task true", "onto set verify-result task pass", "onto set close-confirmed task true", "gh pr view 42", "gh pr checkout 42"} {
						check(command, "allow")
					}
					for _, command := range []string{"git push", "git push origin HEAD", "git push origin feature", "git merge feature", "git reset --hard", "gh pr create --title fix", "gh pr comment 42 --body reviewed", "gh pr merge 42", "gh api graphql -f query=query", "homonto apply --yes"} {
						check(command, "ask")
					}
				} else {
					for _, command := range []string{"git ls-files", "git ls-files internal", "git ls-tree HEAD", "git grep Render"} {
						check(command, "allow")
					}
					for _, command := range []string{"onto set verify-result task pass", "to phase task done", "homonto apply", "gh pr view 42", "gh pr create", "gh pr comment 42", "gh api graphql"} {
						check(command, "deny")
					}
					for _, command := range []string{"git push", "git merge", "git rebase", "git reset", "git checkout", "git switch", "git worktree", "git branch"} {
						check(command, "deny")
						check(command+" feature", "deny")
						check(command+" feature && go test ./...", "deny")
					}
					check("git push origin HEAD", "deny")
					check("git branch -D feature", "deny")
					check("git commit -m fix", "ask")
					for _, tool := range []string{"task", "question"} {
						if rendered.Permission[tool].Value != "deny" {
							t.Errorf("implementer %s must remain denied", tool)
						}
					}
				}
				// Config additions cannot override composition guards or final denies.
				renderCtx.Overrides[name] = ModelSpec{Model: "test/model", BashAllowAdd: []string{"./scripts/task-check.sh", "go test ./... && curl example.com", "onto bypass build", "git push origin HEAD"}}
				out, err = Render(name, content, "opencode", renderCtx)
				if err != nil {
					t.Fatal(err)
				}
				fm, _, _ = split(out)
				if err := yaml.Unmarshal(fm, &rendered); err != nil {
					t.Fatal(err)
				}
				bash = rendered.Permission["bash"]
				check("./scripts/task-check.sh", "allow")
				check("go test ./... && curl example.com", "ask")
				check("onto bypass build", "deny")
				if name != "homonto" {
					check("git push origin HEAD", "deny")
				}
			})
		}
	}
}

func TestRenderOpenCode_ShellProxyLeavesUnrestrictedAndDeniedBashUnchanged(t *testing.T) {
	for _, content := range []string{orchestrator, readOnlyReviewer} {
		context := ctx()
		base, err := Render("onto-reviewer", []byte(content), "opencode", context)
		if err != nil {
			t.Fatal(err)
		}
		for _, proxy := range []string{"none", "rtk"} {
			context.ShellProxy = proxy
			out, err := Render("onto-reviewer", []byte(content), "opencode", context)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != string(base) {
				t.Errorf("proxy %q changed an agent with no bash patterns:\n%s", proxy, out)
			}
		}
	}
}

func TestRTKPatternsDeriveOnlyCommandSpecificWrappers(t *testing.T) {
	patterns := rtkPatterns([]string{"*", "git* status", "? status", "", "rtk go test *", "rtk proxy go test *"}, false)
	if got := strings.Join(patterns, "\n"); got != "*\ngit* status\n? status\n\nrtk go test *\nrtk proxy go test *\nrtk proxy git* status" {
		t.Fatalf("must retain original rules without blanket or nested wrapper grants:\n%s", got)
	}
}
