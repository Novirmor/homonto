package config

import (
	"reflect"
	"testing"
)

func TestIntegrationValidationAndAnyParticipantSelection(t *testing.T) {
	valid := IntegrationCheck{Repos: []string{"api", "web"}, Repo: "api", Cwd: ".", Command: []string{"sh", "check.sh"}}
	c := &Config{SchemaVersion: 2, Repos: map[string]string{"api": "/api", "web": "/web"}, Integration: map[string]IntegrationCheck{"pair": valid}}
	if err := validateIntegration(c); err != nil {
		t.Fatal(err)
	}
	for _, changed := range [][]string{{"api"}, {"web"}, {"api", "web"}} {
		if got := c.IntegrationChecksFor(changed); !reflect.DeepEqual(got, []string{"pair"}) {
			t.Fatalf("selection for %v = %v", changed, got)
		}
	}
	if got := c.IntegrationChecksFor([]string{"other"}); len(got) != 0 {
		t.Fatal(got)
	}
	for name, change := range map[string]func(*IntegrationCheck){
		"undeclared":     func(c *IntegrationCheck) { c.Repos = []string{"other"} },
		"duplicate":      func(c *IntegrationCheck) { c.Repos = []string{"api", "api"} },
		"execution repo": func(c *IntegrationCheck) { c.Repo = "other" },
		"escape":         func(c *IntegrationCheck) { c.Cwd = "../outside" },
		"absolute":       func(c *IntegrationCheck) { c.Cwd = "/outside" },
		"empty cwd":      func(c *IntegrationCheck) { c.Cwd = "" },
		"empty argv":     func(c *IntegrationCheck) { c.Command = nil },
		"nul argv":       func(c *IntegrationCheck) { c.Command = []string{"sh", "bad\x00"} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			change(&bad)
			c.Integration["pair"] = bad
			if err := validateIntegration(c); err == nil {
				t.Fatal("invalid integration accepted")
			}
		})
	}
	c.Integration["pair"] = valid
	c.SchemaVersion = 1
	if err := validateIntegration(c); err == nil {
		t.Fatal("legacy integration accepted")
	}
}
