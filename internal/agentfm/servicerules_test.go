package agentfm

import (
	"strings"
	"testing"
)

func TestServiceRulesOnlyTighten(t *testing.T) {
	ctx := &RenderContext{
		Overrides: map[string]ModelSpec{"lead": {Model: "m"}, "worker": {Model: "m"}, "reader": {Model: "m"}},
		ServiceRules: ServiceRules{
			CoordinatorAsk:  []string{"tracker delete*", "git push --force*"},
			CoordinatorDeny: []string{"tracker purge*"},
			WorkerDeny:      []string{"tracker *"},
		},
	}
	render := func(name, block string) string {
		t.Helper()
		out, err := Render(name, []byte("---\nname: "+name+"\nhomonto:\n"+block+"---\nbody\n"), "opencode", ctx)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	lead := render("lead", "  primary: true\n  bash_default: allow\n  bash_ask: [\"git push --force*\"]\n")
	for _, want := range []string{`"tracker delete*": ask`, `"tracker purge*": deny`} {
		if !strings.Contains(lead, want) {
			t.Errorf("coordinator missing %s:\n%s", want, lead)
		}
	}
	if strings.Count(lead, `"git push --force*"`) != 1 {
		t.Errorf("a contributed rule the agent already has rendered twice:\n%s", lead)
	}
	if strings.Contains(lead, `"tracker *": deny`) {
		t.Errorf("worker denies reached the coordinator:\n%s", lead)
	}

	worker := render("worker", "  bash_default: allow\n")
	if !strings.Contains(worker, `"tracker *": deny`) || strings.Contains(worker, "tracker delete*") {
		t.Errorf("worker rules wrong:\n%s", worker)
	}

	// An agent with no shell gains nothing (and is not an error).
	reader := render("reader", "  bash: false\n  read_only: true\n")
	if strings.Contains(reader, "tracker") || !strings.Contains(reader, "bash: deny") {
		t.Errorf("shell-less agent changed:\n%s", reader)
	}
}

func TestRuntimeProviderToolsFollowInstalledServices(t *testing.T) {
	ctx := &RenderContext{
		Overrides:        map[string]ModelSpec{"homonto": {Model: "m"}},
		RuntimeProviders: []string{"bitbucket", "gitlab"},
	}
	out, err := Render("homonto", []byte("---\nname: homonto\nhomonto:\n  primary: true\n  bash: true\n---\nbody\n"), "opencode", ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, tool := range []string{
		"homonto_gitlab_draft", "homonto_gitlab_status", "homonto_gitlab_publish", "homonto_gitlab_read",
		"homonto_bitbucket_draft", "homonto_bitbucket_status", "homonto_bitbucket_publish", "homonto_bitbucket_read",
	} {
		if !strings.Contains(got, tool+": allow") {
			t.Errorf("missing permission for %s:\n%s", tool, got)
		}
	}
	if strings.Contains(got, "homonto_github_draft") {
		t.Errorf("uninstalled GitHub service received runtime permissions:\n%s", got)
	}
}
