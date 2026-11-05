package starter

import (
	"errors"
	"strings"
	"testing"
)

func full() Selection {
	return Selection{
		Hosts:     []string{"bitbucket", "github"},
		Harnesses: []string{"claude", "opencode"},
		Engines:   []string{"no", "onto"},
		Models:    map[string]string{"opencode": "anthropic/claude-sonnet-5-5", "claude": "sonnet"},
		Tmp:       true,
		Repos:     [][2]string{{"api", "../api"}},
	}
}

func TestRenderIsOrderedAndCredentialFree(t *testing.T) {
	doc, err := Render(full())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`harnesses = ["opencode", "claude"]`,
		"[engines.onto]", "[engines.no]",
		"[workflows.h-workflow-gh]", "[workflows.h-workflow-bb]",
		"[routing.default.opencode]\nmodel = \"anthropic/claude-sonnet-5-5\"",
		"[routing.default.claude]\nmodel = \"sonnet\"",
		"[tmp]\ndir = \".tmp\"", "[repos]\napi = \"../api\"",
		"HOMONTO_BITBUCKET_TOKEN", "gh auth login",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("rendered config lacks %q:\n%s", want, doc)
		}
	}
	// Display order, not selection order.
	if strings.Index(doc, "[engines.onto]") > strings.Index(doc, "[engines.no]") ||
		strings.Index(doc, "h-workflow-gh]") > strings.Index(doc, "h-workflow-bb]") {
		t.Errorf("sections are not in display order:\n%s", doc)
	}
	for _, never := range []string{"api_key", "token =", "password", "${"} {
		if strings.Contains(doc, never) {
			t.Errorf("rendered config carries a credential-like value %q", never)
		}
	}
	again, _ := Render(full())
	if again != doc {
		t.Error("render is not deterministic")
	}
}

func TestValidateRefusesBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Selection)
		want   string
	}{
		"no harness":         {func(s *Selection) { s.Harnesses = nil }, "select at least one harness"},
		"unknown harness":    {func(s *Selection) { s.Harnesses = []string{"codex"} }, `harness "codex" is not one of opencode, claude`},
		"unknown host":       {func(s *Selection) { s.Hosts = []string{"gitea"} }, `host "gitea" is not one of github, gitlab, bitbucket`},
		"unknown engine":     {func(s *Selection) { s.Engines = []string{"h"} }, `engine "h" is not one of onto, to, no`},
		"duplicate":          {func(s *Selection) { s.Engines = []string{"to", "to"} }, `engine "to" is selected twice`},
		"nothing to run":     {func(s *Selection) { s.Hosts, s.Engines = nil, nil }, "select at least one engine"},
		"missing model":      {func(s *Selection) { delete(s.Models, "claude") }, "--model claude=<model> is required"},
		"model for unchosen": {func(s *Selection) { s.Harnesses = []string{"opencode"} }, "--model claude=… names a harness that is not selected"},
		"numeric repo":       {func(s *Selection) { s.Repos = [][2]string{{"42", "../x"}} }, "not numeric-only"},
		"repo without path":  {func(s *Selection) { s.Repos = [][2]string{{"api", ""}} }, "has no path"},
	} {
		t.Run(name, func(t *testing.T) {
			s := full()
			tc.mutate(&s)
			_, err := Render(s)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	s := full()
	s.Harnesses = []string{"codex"}
	if err := s.Validate(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("an unsupported harness is not ErrUnsupported: %v", err)
	}
}

func TestCellsCoverEveryHostAndHarness(t *testing.T) {
	cells := Cells()
	if len(cells) != (len(Hosts)+1)*len(Harnesses) {
		t.Fatalf("cells = %v", cells)
	}
}
