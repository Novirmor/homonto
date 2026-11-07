package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPreviewValidationAndBudgets(t *testing.T) {
	valid := Preview{Repo: "web", Dockerfile: "Dockerfile", Port: 3000}
	c := &Config{SchemaVersion: 2, Repos: map[string]string{"web": "/web", "api": "/api"}, Preview: map[string]Preview{"site": valid}}
	if err := validatePreviews(c); err != nil {
		t.Fatal(err)
	}
	b := valid.Budget()
	if b.CPUs != DefaultPreviewCPUs || b.MemoryBytes != 512<<20 || b.Pids != DefaultPreviewPids || b.TTL != DefaultPreviewTTL || b.Startup != DefaultPreviewStartup {
		t.Fatalf("default budget = %+v", b)
	}
	custom := Preview{Repo: "web", Image: "nginx:1", Command: []string{"nginx"}, Port: 80, CPUs: 0.25, Memory: "1g", Pids: 10, TTL: "2h", Startup: "30s"}
	if got := custom.Budget(); got.CPUs != 0.25 || got.MemoryBytes != 1<<30 || got.Pids != 10 || got.TTL != 2*time.Hour || got.Startup != 30*time.Second {
		t.Fatalf("custom budget = %+v", got)
	}
	if got := c.PreviewsFor([]string{"web"}); !reflect.DeepEqual(got, []string{"site"}) {
		t.Fatalf("selection = %v", got)
	}
	if got := c.PreviewsFor([]string{"api"}); len(got) != 0 {
		t.Fatalf("selection for api = %v", got)
	}
	for name, tc := range map[string]struct {
		change func(*Preview)
		want   string
	}{
		"undeclared repo":     {func(p *Preview) { p.Repo = "other" }, "not a declared repository"},
		"no source":           {func(p *Preview) { p.Dockerfile = "" }, "exactly one"},
		"both sources":        {func(p *Preview) { p.Image = "nginx" }, "exactly one"},
		"escaping dockerfile": {func(p *Preview) { p.Dockerfile = "../Dockerfile" }, "relative"},
		"escaping context":    {func(p *Preview) { p.Context = "/abs" }, "relative"},
		"command with build":  {func(p *Preview) { p.Command = []string{"x"} }, "only with image"},
		"no port":             {func(p *Preview) { p.Port = 0 }, "port"},
		"relative path":       {func(p *Preview) { p.Path = "health" }, "start with /"},
		"too many cpus":       {func(p *Preview) { p.CPUs = 64 }, "cpus"},
		"tiny memory":         {func(p *Preview) { p.Memory = "1m" }, "between 32m and 8g"},
		"bad memory":          {func(p *Preview) { p.Memory = "lots" }, "positive size"},
		"too many pids":       {func(p *Preview) { p.Pids = 1 << 20 }, "pids"},
		"endless ttl":         {func(p *Preview) { p.TTL = "48h" }, "ttl"},
		"bad startup":         {func(p *Preview) { p.Startup = "soon" }, "startup"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			tc.change(&bad)
			c.Preview = map[string]Preview{"site": bad}
			if err := validatePreviews(c); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	c.Preview = map[string]Preview{"Bad_Name": valid}
	if err := validatePreviews(c); err == nil {
		t.Fatal("a name Docker cannot use was accepted")
	}
	c.SchemaVersion, c.Preview = 1, map[string]Preview{"site": valid}
	if err := validatePreviews(c); err == nil {
		t.Fatal("preview accepted without schema 2")
	}
}
