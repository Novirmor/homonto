package routing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/config"
)

type fakeCatalog struct {
	models []string
	err    error
	calls  int
}

func (f *fakeCatalog) Models(context.Context) ([]string, error) {
	f.calls++
	return f.models, f.err
}

func num(v float64) *float64 { return &v }

var described = map[string]config.ModelInfo{
	"anthropic/big":   {Cost: num(15), Latency: num(4), Tags: []string{"reasoning", "tools"}},
	"anthropic/small": {Cost: num(1), Latency: num(1), Tags: []string{"tools"}},
	"openai/mid":      {Cost: num(5), Latency: num(2), Tags: []string{"reasoning", "tools"}},
	"local/free":      {Cost: num(0), Tags: []string{"tools"}},
}

func catalogOf() *fakeCatalog {
	return &fakeCatalog{models: []string{"anthropic/big", "anthropic/small", "openai/mid", "local/free", "other/undescribed"}}
}

func auto(p config.RoutePolicy) config.RoutePolicy {
	p.Mode = config.RouteAuto
	if p.Fallback == "" {
		p.Fallback = "anthropic/small"
	}
	return p
}

func TestStaticNeverDiscovers(t *testing.T) {
	cat := catalogOf()
	r := &Resolver{Discover: cat}
	sel, err := r.Resolve(context.Background(), "x", config.RoutePolicy{Model: "p/m", Variant: "high"})
	if err != nil || sel.Model != "p/m" || sel.Variant != "high" || sel.Mode != config.RouteStatic {
		t.Fatalf("sel = %+v, %v", sel, err)
	}
	if cat.calls != 0 {
		t.Fatalf("static policy called discovery %d times", cat.calls)
	}
	if state, _, _ := r.DiscoveryState(); state != DiscoveryNotRun {
		t.Fatalf("state = %s", state)
	}
}

func TestAutoSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy config.RoutePolicy
		want   string
	}{
		{"listed order is lexical without candidates", config.RoutePolicy{}, "anthropic/big"},
		{"candidates keep their order", config.RoutePolicy{Candidates: []string{"openai/mid", "anthropic/big"}}, "openai/mid"},
		{"unavailable candidates are skipped", config.RoutePolicy{Candidates: []string{"gone/model", "openai/mid"}}, "openai/mid"},
		{"providers filter", config.RoutePolicy{Providers: []string{"openai"}}, "openai/mid"},
		{"needs filter on described tags", config.RoutePolicy{Needs: []string{"reasoning"}, Prefer: config.PreferCost}, "openai/mid"},
		{"cheapest", config.RoutePolicy{Prefer: config.PreferCost}, "local/free"},
		{"cost cap excludes undescribed models", config.RoutePolicy{MaxCost: num(2), Providers: []string{"anthropic", "other"}}, "anthropic/small"},
		{"fastest; undescribed latency sorts last", config.RoutePolicy{Prefer: config.PreferLatency, Providers: []string{"anthropic", "local"}}, "anthropic/small"},
		{"latency cap", config.RoutePolicy{MaxLatency: num(3), Needs: []string{"reasoning"}}, "openai/mid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Resolver{Discover: catalogOf(), Models: described}
			sel, err := r.Resolve(context.Background(), "x", auto(tc.policy))
			if err != nil || sel.Model != tc.want || sel.Fallback {
				t.Fatalf("sel = %+v, %v; want %s", sel, err, tc.want)
			}
			if !strings.HasPrefix(sel.Reason, "auto: ") {
				t.Fatalf("reason = %q", sel.Reason)
			}
		})
	}
}

func TestAutoFallback(t *testing.T) {
	t.Run("discovery failure", func(t *testing.T) {
		r := &Resolver{Discover: &fakeCatalog{err: errors.New("opencode: not logged in")}}
		sel, err := r.Resolve(context.Background(), "x", auto(config.RoutePolicy{Fallback: "p/safe"}))
		if err != nil || sel.Model != "p/safe" || !sel.Fallback || !strings.Contains(sel.Reason, "discovery failed (opencode: not logged in)") {
			t.Fatalf("sel = %+v, %v", sel, err)
		}
		if state, _, derr := r.DiscoveryState(); state != DiscoveryFailed || derr == nil {
			t.Fatalf("state = %s %v", state, derr)
		}
	})
	t.Run("empty catalog counts as failure", func(t *testing.T) {
		r := &Resolver{Discover: &fakeCatalog{}}
		sel, _ := r.Resolve(context.Background(), "x", auto(config.RoutePolicy{Fallback: "p/safe"}))
		if !sel.Fallback || !strings.Contains(sel.Reason, "reported no models") {
			t.Fatalf("sel = %+v", sel)
		}
	})
	t.Run("nothing qualifies", func(t *testing.T) {
		r := &Resolver{Discover: catalogOf(), Models: described}
		sel, err := r.Resolve(context.Background(), "x", auto(config.RoutePolicy{Needs: []string{"vision"}}))
		if err != nil || sel.Model != "anthropic/small" || !sel.Fallback || !strings.Contains(sel.Reason, "none of 5 available models") {
			t.Fatalf("sel = %+v, %v", sel, err)
		}
	})
	t.Run("unavailable fallback is an error", func(t *testing.T) {
		r := &Resolver{Discover: catalogOf(), Models: described}
		_, err := r.Resolve(context.Background(), "routing.default.opencode", auto(config.RoutePolicy{Needs: []string{"vision"}, Fallback: "gone/model"}))
		if err == nil || !strings.Contains(err.Error(), `routing.default.opencode: no available model satisfies the policy, and its fallback "gone/model" is not among the 5 models`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no discoverer", func(t *testing.T) {
		sel, _ := (&Resolver{}).Resolve(context.Background(), "x", auto(config.RoutePolicy{Fallback: "p/safe"}))
		if !sel.Fallback || sel.Model != "p/safe" {
			t.Fatalf("sel = %+v", sel)
		}
	})
}

func TestDiscoveryRunsOnce(t *testing.T) {
	cat := catalogOf()
	r := &Resolver{Discover: cat, Models: described}
	for range 3 {
		if _, err := r.Resolve(context.Background(), "x", auto(config.RoutePolicy{})); err != nil {
			t.Fatal(err)
		}
	}
	if cat.calls != 1 {
		t.Fatalf("calls = %d", cat.calls)
	}
	if state, n, _ := r.DiscoveryState(); state != DiscoveryOK || n != 5 {
		t.Fatalf("state = %s %d", state, n)
	}
}

func TestParseModelList(t *testing.T) {
	got := ParseModelList([]byte("Loading providers...\nanthropic/claude-sonnet-5\n\nopenrouter/openai/gpt-6\nanthropic/claude-sonnet-5\nnot a model\n"))
	if strings.Join(got, ",") != "anthropic/claude-sonnet-5,openrouter/openai/gpt-6" {
		t.Fatalf("got %v", got)
	}
}
