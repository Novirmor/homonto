package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/resourcepath"
)

const intentAgent = "---\nname: mine\ndescription: a custom reviewer\nmode: subagent\nhomonto:\n  read_only: true\n  bash: false\n  network: false\n---\nReview the change.\n"

// externalAgentRepo writes a config declaring one local subagent with the
// given content and config tail, under the engine's content dir.
func externalAgentRepo(t *testing.T, content, tail string) (home, repo string) {
	t.Helper()
	home, repo = t.TempDir(), t.TempDir()
	for path, data := range map[string]string{
		filepath.Join(repo, "content", "subagents", "mine.md"): content,
		filepath.Join(repo, "homonto.toml"):                    "[subagents.mine]\nsource = \"local:mine\"\nscope = \"project\"\n" + tail,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, repo
}

func TestExternalIntentAgentRendersWithItsModelOverride(t *testing.T) {
	home, repo := externalAgentRepo(t, intentAgent, "[subagents.mine.opencode]\nmodel = \"openai/gpt-5\"\nvariant = \"high\"\n")
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	rendered := filepath.Join(repo, ".homonto", "rendered", "subagents", "mine.opencode.md")
	data, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatalf("rendered agent missing: %v", err)
	}
	got := string(data)
	for _, want := range []string{`model: "openai/gpt-5"`, `variant: "high"`, "permission:", "edit: deny", "bash: deny", "webfetch: deny"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered agent lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "homonto:") {
		t.Errorf("intent block leaked into the rendered agent:\n%s", got)
	}
	// It is not a builtin role: no coordinator tools, no engine-computed
	// directory grants.
	for _, banned := range []string{"homonto_status", "external_directory"} {
		if strings.Contains(got, banned) {
			t.Errorf("external agent gained %s:\n%s", banned, got)
		}
	}
	link := filepath.Join(resourcepath.Dir(resourcepath.Subagent, "opencode", "project", home, repo), "mine.md")
	if linked, err := os.ReadFile(link); err != nil || string(linked) != got {
		t.Fatalf("project agent %s does not serve the rendered variant: %v", link, err)
	}
	// A second plan converges.
	e = buildEngine(t, home, repo)
	for _, cs := range mustPlan(t, e) {
		for _, c := range cs.Changes {
			if c.Action != "noop" {
				t.Errorf("not converged after apply: %+v", c)
			}
		}
	}
}

func TestExternalAgentRenderErrors(t *testing.T) {
	for _, tc := range []struct {
		name, content, tail, want string
	}{
		{"verbatim agent with an override", "---\nname: mine\nmode: subagent\n---\nbody\n", "[subagents.mine.opencode]\nmodel = \"openai/gpt-5\"\n", "has no homonto: intent block, so it is projected verbatim"},
		{"intent agent without a model", intentAgent, "", "set [subagents.mine.opencode] model"},
		{"malformed intent", strings.Replace(intentAgent, "read_only: true", "read_only: true\n  sandbox: strict", 1), "[subagents.mine.opencode]\nmodel = \"m/x\"\n", "subagents.mine"},
		{"intent agent with its own model line", strings.Replace(intentAgent, "mode: subagent", "mode: subagent\nmodel: other/model", 1), "[subagents.mine.opencode]\nmodel = \"m/x\"\n", "declares model in its frontmatter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := externalAgentRepo(t, tc.content, tc.tail)
			_, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestVerbatimExternalAgentStaysVerbatim(t *testing.T) {
	content := "---\nname: mine\nmode: subagent\nmodel: other/model\n---\nbody\n"
	home, repo := externalAgentRepo(t, content, "")
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(resourcepath.Dir(resourcepath.Subagent, "opencode", "project", home, repo), "mine.md")
	data, err := os.ReadFile(link)
	if err != nil || string(data) != content {
		t.Fatalf("verbatim agent changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".homonto", "rendered")); !os.IsNotExist(err) {
		t.Fatalf("a verbatim agent produced a render: %v", err)
	}
}
