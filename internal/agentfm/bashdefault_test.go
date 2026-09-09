package agentfm

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These are ordered permission-request contracts, not shell parsing or a sandbox.
func TestBashDefaultPermissionRules(t *testing.T) {
	for _, policy := range []string{"", "ask", "allow"} {
		for _, proxy := range []string{"", "none", "rtk"} {
			t.Run("default="+policy+"/proxy="+proxy, func(t *testing.T) {
				trusted := policy == "allow"
				h := Homonto{
					BashDefault: policy,
					BashAllow:   []string{"git push", "git *", "onto bypass build", "onto *"},
					BashAsk:     []string{"git push", "git push*", "git push", "onto bypass build", "*publish*"},
					BashDeny:    []string{"onto bypass build", "onto bypass*", "onto bypass build"},
				}
				doc, err := yaml.Marshal(map[string]any{"homonto": h})
				if err != nil {
					t.Fatal(err)
				}
				context := &RenderContext{ShellProxy: proxy, Overrides: map[string]ModelSpec{
					"custom": {Model: "test/model", BashAllowAdd: []string{
						"./scripts/check.sh", "git push", "git push origin HEAD", "rtk git push",
						"rtk proxy git push", "onto bypass build", "rtk onto bypass build",
						"rtk proxy onto bypass build", "git status && ./scripts/check.sh",
					}},
				}}
				out, err := Render("custom", []byte("---\n"+string(doc)+"---\nbody\n"), "opencode", context)
				if err != nil {
					t.Fatal(err)
				}
				fm, _, _ := split(out)
				var decoded map[string]any
				if err := yaml.Unmarshal(fm, &decoded); err != nil {
					t.Fatalf("duplicate or invalid permission keys: %v\n%s", err, out)
				}
				var ordered struct {
					Permission map[string]yaml.Node `yaml:"permission"`
				}
				if err := yaml.Unmarshal(fm, &ordered); err != nil {
					t.Fatal(err)
				}
				bash := ordered.Permission["bash"]
				if bash.Kind != yaml.MappingNode || bash.Content[0].Value != "*" {
					t.Fatalf("missing first-position baseline:\n%s", out)
				}
				baseline := "ask"
				if trusted {
					baseline = "allow"
				}
				if bash.Content[1].Value != baseline {
					t.Fatalf("baseline = %s, want %s", bash.Content[1].Value, baseline)
				}
				check := func(request, want string) {
					t.Helper()
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
				for _, request := range []string{
					"./scripts/setup.sh", "python script.py", "node script.js", "bash -c true",
					"git clone https://example.com/repo checkout && mkdir -p build",
					"npm install && npm test", "./scripts/a.sh; ./scripts/b.sh", "make test | tee results",
					"result=$(./scripts/check.sh)", "result=`./scripts/check.sh`", "./scripts/check.sh > output",
					"./scripts/check.sh < input", "./scripts/a.sh\n./scripts/b.sh", "./scripts/a.sh || true",
					"rtk unknown-command", "rtk exec ./scripts/check.sh",
					// Arbitrary launchers are not inspected for protected commands.
					"bash -c 'git push'", "python -c 'import os; os.system(\"git push\")'",
				} {
					check(request, baseline)
				}
				check("git status", "allow")
				check("./scripts/check.sh", "allow")
				check("git status && ./scripts/check.sh", baseline)
				for _, prefix := range []string{"", "rtk ", "rtk proxy "} {
					check(prefix+"git push", "ask")
					check(prefix+"git push origin HEAD", "ask")
					check(prefix+"publish artifacts", "ask")
					// Guarded mode without RTK preserves the old deny derivation.
					want := "deny"
					if prefix != "" && !trusted && proxy != "rtk" {
						want = "ask" // protected ask still overrides config additions
					}
					check(prefix+"onto bypass build", want)
				}
				for _, pattern := range bashCompositionGuards {
					present := strings.Contains(string(out), fmt.Sprintf("    %q: ask", pattern))
					if present == trusted {
						t.Errorf("composition guard %q present = %v, trusted = %v", pattern, present, trusted)
					}
				}
				for _, pattern := range []string{"rtk *", "rtk proxy *", "rtk exec *"} {
					if strings.Contains(string(out), fmt.Sprintf("    %q: allow", pattern)) {
						t.Errorf("derived blanket wrapper grant %q", pattern)
					}
				}
				patterns := []string{"*", "git *", "./scripts/check.sh", "git push origin HEAD", "git push*", "git push", "rtk git push", "rtk proxy git push"}
				if !trusted {
					patterns = append(patterns, "*;*")
				}
				patterns = append(patterns, "onto bypass*", "onto bypass build")
				previous := -1
				for _, pattern := range patterns {
					index := strings.Index(string(out), fmt.Sprintf("    %q:", pattern))
					if index <= previous {
						t.Errorf("rule %q missing or not in last-occurrence order:\n%s", pattern, out)
					}
					previous = index
				}
			})
		}
	}
}

func TestBashDefaultBackwardCompatibility(t *testing.T) {
	for _, proxy := range []string{"", "none", "rtk"} {
		context := &RenderContext{ShellProxy: proxy, Overrides: map[string]ModelSpec{"custom": {Model: "test/model"}}}
		for _, rules := range []string{"", "  bash_allow: [\"git status*\"]\n  bash_deny: [\"git push*\"]\n"} {
			content := "---\nhomonto:\n  dialogs: false\n" + rules + "---\nbody\n"
			base, err := Render("custom", []byte(content), "opencode", context)
			if err != nil {
				t.Fatal(err)
			}
			for _, policy := range []string{`""`, "ask"} {
				updated := strings.Replace(content, "homonto:\n", "homonto:\n  bash_default: "+policy+"\n", 1)
				out, err := Render("custom", []byte(updated), "opencode", context)
				if err != nil {
					t.Fatal(err)
				}
				if policy == "ask" && rules == "" {
					if !strings.Contains(string(out), "  bash:\n    \"*\": ask\n") {
						t.Fatalf("explicit ask without lists must set the baseline:\n%s", out)
					}
				} else if string(out) != string(base) {
					t.Errorf("guarded policy changed existing output:\n%s\nwant:\n%s", out, base)
				}
			}
			if rules == "" && strings.Contains(string(base), "  bash:") {
				t.Errorf("omitted shell intent must remain inherited:\n%s", base)
			}
		}
		out, err := Render("custom", []byte("---\nhomonto:\n  read_only: true\n  bash: false\n  spawn: []\n---\nbody\n"), "opencode", context)
		if err != nil {
			t.Fatal(err)
		}
		want := "---\nmode: subagent\nmodel: \"test/model\"\npermission:\n  edit: deny\n  bash: deny\n  question: deny\n  task: deny\n---\nbody\n"
		if string(out) != want {
			t.Errorf("read-only shell-denied output changed:\n%s", out)
		}
	}
}

func TestBashDefaultStandaloneAndAskOnly(t *testing.T) {
	for _, tc := range []struct {
		intent string
		want   []string
	}{
		{"  bash_default: allow\n", []string{"  bash:\n    \"*\": allow\n  question: deny"}},
		{"  bash_ask: [\"git push*\"]\n", []string{`"*": ask`, `"git push*": ask`, `"rtk git push*": ask`, `"rtk proxy git push*": ask`}},
		{"  bash_default: allow\n  bash_ask: [\"git push*\"]\n  bash_deny: [\"onto bypass*\"]\n", []string{`"*": allow`, `"rtk git push*": ask`, `"rtk proxy git push*": ask`, `"rtk onto bypass*": deny`, `"rtk proxy onto bypass*": deny`}},
	} {
		out, err := Render("custom", []byte("---\nhomonto:\n"+tc.intent+"---\nbody\n"), "opencode", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(string(out), want) {
				t.Errorf("missing %q:\n%s", want, out)
			}
		}
	}
}

func TestBashDefaultInvalidIntentFailsClosed(t *testing.T) {
	var cases []string
	for _, value := range []string{"null", "~", "", "false", "true", "123", "1.5", "[]", "{}", "[allow]", "deny", "ALLOW", `" allow "`} {
		cases = append(cases, "  bash_default: "+value+"\n")
	}
	for _, field := range []string{"bash_allow", "bash_ask", "bash_deny"} {
		for _, value := range []string{"null", "~", "", "false", "123", "git push", "{}", "[null]", "[~]", "[true]", "[123]", "[1.5]", "[[]]", "[{}]", `[""]`, `[" "]`, `["git push*", null]`} {
			cases = append(cases, "  "+field+": "+value+"\n")
		}
	}
	for _, policy := range []string{"allow", "ask", `""`} {
		cases = append(cases, "  bash: false\n  bash_default: "+policy+"\n")
	}
	cases = append(cases,
		"  bash: false\n  bash_ask: [\"git push*\"]\n",
		"  read_only: true\n  bash_default: allow\n",
		"  read_only: true\n  bash: true\n  bash_default: allow\n",
		"  bash_default: allow\n  bash_default: ask\n",
	)
	for _, intent := range cases {
		t.Run(strings.TrimSpace(intent), func(t *testing.T) {
			content := []byte("---\nhomonto:\n" + intent + "---\nbody\n")
			if needs, err := NeedsTransform(content); err == nil {
				t.Errorf("NeedsTransform = %v, nil; malformed intent must fail closed", needs)
			}
			if out, err := Render("custom", content, "opencode", nil); err == nil || out != nil {
				t.Errorf("Render = %q, %v; malformed intent must not project", out, err)
			}
		})
	}
}

func TestBashDefaultYAMLAliasCompatibility(t *testing.T) {
	var cases []struct{ anchors, aliased, literal string }
	for _, policy := range []string{`""`, "ask", "allow"} {
		cases = append(cases, struct{ anchors, aliased, literal string }{
			"shell_default: &default " + policy + "\n",
			"  bash_default: *default\n", "  bash_default: " + policy + "\n",
		})
	}
	for _, field := range []string{"bash_allow", "bash_ask", "bash_deny"} {
		cases = append(cases,
			struct{ anchors, aliased, literal string }{
				"shell_reads: &reads [\"git status\", \"git diff\"]\n",
				"  " + field + ": *reads\n", "  " + field + ": [\"git status\", \"git diff\"]\n",
			},
			struct{ anchors, aliased, literal string }{
				"shell_read: &read git status\n",
				"  " + field + ": [*read, \"git diff\"]\n", "  " + field + ": [\"git status\", \"git diff\"]\n",
			},
		)
	}
	// Aliased lists may themselves contain aliased string entries.
	cases = append(cases, struct{ anchors, aliased, literal string }{
		"shell_read: &read git status\nshell_reads: &reads [*read, \"git diff\"]\n",
		"  bash_allow: *reads\n", "  bash_allow: [\"git status\", \"git diff\"]\n",
	})
	for _, tc := range cases {
		for _, proxy := range []string{"", "none", "rtk"} {
			t.Run(strings.TrimSpace(tc.aliased)+"/"+tc.anchors+"/proxy="+proxy, func(t *testing.T) {
				context := &RenderContext{ShellProxy: proxy, Overrides: map[string]ModelSpec{"custom": {Model: "test/model"}}}
				content := []byte("---\n" + tc.anchors + "homonto:\n" + tc.aliased + "---\nbody\n")
				if needs, err := NeedsTransform(content); err != nil || !needs {
					t.Fatalf("NeedsTransform = %v, %v", needs, err)
				}
				out, err := Render("custom", content, "opencode", context)
				if err != nil {
					t.Fatal(err)
				}
				literal := []byte("---\n" + tc.anchors + "homonto:\n" + tc.literal + "---\nbody\n")
				want, err := Render("custom", literal, "opencode", context)
				if err != nil {
					t.Fatal(err)
				}
				if string(out) != string(want) {
					t.Errorf("alias changed literal policy output:\n%s\nwant:\n%s", out, want)
				}
			})
		}
	}
}

func TestBashDefaultInvalidYAMLAliasesFailClosed(t *testing.T) {
	for _, field := range []string{"bash_default", "bash_allow", "bash_ask", "bash_deny"} {
		for _, target := range []string{"null", "false", "123", "1.5", "{}", "[*target]"} {
			for _, value := range []string{"*target", "[*target]"} {
				t.Run(field+"/"+target+"/"+value, func(t *testing.T) {
					content := []byte("---\nshell_target: &target " + target + "\nhomonto:\n  " + field + ": " + value + "\n---\nbody\n")
					if needs, err := NeedsTransform(content); err == nil {
						t.Errorf("NeedsTransform = %v, nil; invalid alias must fail closed", needs)
					}
					if out, err := Render("custom", content, "opencode", nil); err == nil || out != nil {
						t.Errorf("Render = %q, %v; invalid alias must not project", out, err)
					}
				})
			}
		}
		t.Run(field+"/undefined", func(t *testing.T) {
			content := []byte("---\nhomonto:\n  " + field + ": *missing\n---\nbody\n")
			if _, err := NeedsTransform(content); err == nil {
				t.Fatal("undefined alias must retain the YAML decoder error")
			}
			if out, err := Render("custom", content, "opencode", nil); err == nil || out != nil {
				t.Errorf("Render = %q, %v; undefined alias must not project", out, err)
			}
		})
	}
}

func TestBashDefaultResolveYAMLAlias(t *testing.T) {
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "allow"}
	alias := &yaml.Node{Kind: yaml.AliasNode, Alias: scalar}
	chain := &yaml.Node{Kind: yaml.AliasNode, Alias: alias}
	missing := &yaml.Node{Kind: yaml.AliasNode}
	self := &yaml.Node{Kind: yaml.AliasNode}
	self.Alias = self
	cycle := &yaml.Node{Kind: yaml.AliasNode}
	cycleTail := &yaml.Node{Kind: yaml.AliasNode, Alias: cycle}
	cycle.Alias = cycleTail
	for _, tc := range []struct {
		name        string
		input, want *yaml.Node
	}{
		{"nil", nil, nil},
		{"scalar", scalar, scalar},
		{"alias", alias, scalar},
		{"chain", chain, scalar},
		{"missing", missing, nil},
		{"self cycle", self, nil},
		{"indirect cycle", cycle, nil},
		{"indirect cycle tail", cycleTail, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before yaml.Node
			if tc.input != nil {
				before = *tc.input
			}
			if got := resolveYAMLAlias(tc.input); got != tc.want {
				t.Errorf("resolved node = %p, want %p", got, tc.want)
			}
			if tc.input != nil && !reflect.DeepEqual(*tc.input, before) {
				t.Fatal("alias resolution mutated the syntax node")
			}
		})
	}
}
