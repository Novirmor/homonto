package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Novirmor/homonto/internal/decision"
)

const noEngine = "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n" + noModels

const decisionProviders = `
[decisions]
default = "openai"
min_confidence = 0.6
timeout = "10s"

[decisions.providers.openai]
protocol = "openai-decisions"
model = "gpt-6-luna"
api_key_env = "OPENAI_API_KEY"

[decisions.providers.jev]
protocol = "systemone"
endpoint = "https://openrouter.ai/api/v1/systemone"
model = "typesafe/jev-1.14"
api_key_env = "OPENROUTER_API_KEY"

[decisions.points."no.next-step"]
provider = "jev"
min_confidence = 0.8
`

func TestDecisionRoutes(t *testing.T) {
	c, err := loadRouting(t, noEngine+decisionProviders)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.DecisionRouteFor("no.next-step")
	if err != nil || r.ProviderName != "jev" || r.Endpoint != "https://openrouter.ai/api/v1/systemone" || *r.MinConfidence != 0.8 || r.Timeout != 10*time.Second {
		t.Fatalf("listed point: %+v %v", r, err)
	}
	r, err = c.DecisionRouteFor("no.other")
	if err != nil || r.ProviderName != "openai" || r.Endpoint != "https://api.openai.com/v1/decisions" || *r.MinConfidence != 0.6 {
		t.Fatalf("default point: %+v %v", r, err)
	}
	empty, err := loadRouting(t, noEngine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.DecisionRouteFor("no.x"); !errors.Is(err, decision.ErrNoProvider) {
		t.Fatalf("unconfigured: %v", err)
	}
	if r, _ := empty.DecisionRouteFor("no.x"); r.Timeout != 0 {
		t.Fatal("unexpected route")
	}
}

func TestDecisionConfigRejects(t *testing.T) {
	provider := func(body string) string {
		return noEngine + "[decisions.providers.p]\n" + body
	}
	for _, tc := range []struct{ body, want string }{
		{provider("protocol = \"carrier-pigeon\"\nmodel = \"m\"\napi_key_env = \"K\"\n"), `protocol "carrier-pigeon" is not one of openai-decisions, systemone`},
		{provider("protocol = \"systemone\"\nmodel = \"m\"\napi_key_env = \"K\"\n"), "endpoint is required for protocol systemone"},
		{provider("protocol = \"systemone\"\nendpoint = \"http://example.com/v1/systemone\"\nmodel = \"m\"\napi_key_env = \"K\"\n"), "must use https"},
		{provider("protocol = \"systemone\"\nendpoint = \"https://user:pw@example.com/\"\nmodel = \"m\"\napi_key_env = \"K\"\n"), "must not carry credentials"},
		{provider("protocol = \"openai-decisions\"\napi_key_env = \"K\"\n"), "model is required"},
		{provider("protocol = \"openai-decisions\"\nmodel = \"m\"\napi_key_env = \"sk-live-abc\"\n"), "api_key_env must name an environment variable"},
		{provider("protocol = \"openai-decisions\"\nmodel = \"m\"\napi_key_env = \"K\"\napi_key = \"sk\"\n"), "unknown configuration fields"},
		{noEngine + "[decisions]\ndefault = \"ghost\"\n", `decisions.default "ghost" names no`},
		{noEngine + "[decisions]\nmin_confidence = 1.5\n", "decisions.min_confidence must be in [0, 1]"},
		{noEngine + "[decisions]\ntimeout = \"forever\"\n", "decisions.timeout"},
		{noEngine + "[decisions.points.\"nopoint\"]\nprovider = \"p\"\n", "must be <package>.<point>"},
		{noEngine + "[decisions.points.\"ghost.x\"]\nprovider = \"p\"\n", `belongs to package "ghost"`},
		{noEngine + "[decisions.points.\"no.x\"]\n", "names no provider and [decisions] has no default"},
	} {
		if _, err := loadRouting(t, tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q:\nerr = %v\nwant %q", tc.body, err, tc.want)
		}
	}
	// A loopback http endpoint is allowed for a local compatible service.
	if _, err := loadRouting(t, provider("protocol = \"systemone\"\nendpoint = \"http://127.0.0.1:8080/v1/systemone\"\nmodel = \"m\"\napi_key_env = \"K\"\n")); err != nil {
		t.Fatal(err)
	}
}
