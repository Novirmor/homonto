package config

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Novirmor/homonto/internal/decision"
)

// Decisions is the [decisions] table: decision-model providers and which
// one each decision point uses (ADR 0101). Decision models are configured
// apart from execution models: routing never reads this table.
type Decisions struct {
	// Default names the provider for points without their own entry. Empty
	// means only the listed points are served.
	Default string `toml:"default"`
	// MinConfidence below which an answer is reported uncertain, for every
	// point that sets none.
	MinConfidence *float64 `toml:"min_confidence"`
	// Timeout per call, a Go duration ("30s"). Default 30s.
	Timeout   string                      `toml:"timeout"`
	Providers map[string]DecisionProvider `toml:"providers"`
	Points    map[string]DecisionPoint    `toml:"points"`
}

// DecisionProvider is one configured provider.
type DecisionProvider struct {
	Protocol string `toml:"protocol"` // a registered protocol: systemone, openai-decisions
	Endpoint string `toml:"endpoint"` // required when the protocol has no default
	Model    string `toml:"model"`
	// APIKeyEnv names the environment variable holding the credential. The
	// credential itself never appears in the config.
	APIKeyEnv string `toml:"api_key_env"`
}

// DecisionPoint routes one "<package>.<point>".
type DecisionPoint struct {
	Provider      string   `toml:"provider"`
	MinConfidence *float64 `toml:"min_confidence"`
}

// DefaultDecisionTimeout bounds a call when [decisions].timeout is unset.
const DefaultDecisionTimeout = 30 * time.Second

var (
	decisionPoint = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$`)
	providerName  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	envVarName    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

func validateDecisions(c *Config) error {
	d := c.Decisions
	for _, name := range slices.Sorted(maps.Keys(d.Providers)) {
		label := "decisions.providers." + name
		if !providerName.MatchString(name) {
			return fmt.Errorf("parse config: %s: provider names are lowercase letters, digits and '-'", label)
		}
		p := d.Providers[name]
		proto, ok := decision.LookupProtocol(p.Protocol)
		if !ok {
			return fmt.Errorf("parse config: %s protocol %q is not one of %s", label, p.Protocol, strings.Join(decision.Protocols(), ", "))
		}
		endpoint := p.Endpoint
		if endpoint == "" {
			endpoint = proto.DefaultEndpoint()
		}
		if endpoint == "" {
			return fmt.Errorf("parse config: %s endpoint is required for protocol %s (compatible services differ, for example https://openrouter.ai/api/v1/systemone)", label, p.Protocol)
		}
		if err := decisionEndpoint(label, endpoint); err != nil {
			return err
		}
		if p.Model == "" || strings.ContainsAny(p.Model, " \t\n") {
			return fmt.Errorf("parse config: %s model is required (the decision model, configured apart from execution models)", label)
		}
		if !envVarName.MatchString(p.APIKeyEnv) {
			return fmt.Errorf("parse config: %s api_key_env must name an environment variable (for example OPENAI_API_KEY); credentials never go in the config", label)
		}
	}
	if d.Default != "" {
		if _, ok := d.Providers[d.Default]; !ok {
			return fmt.Errorf("parse config: decisions.default %q names no [decisions.providers.%s]", d.Default, d.Default)
		}
	}
	if err := unitInterval("decisions.min_confidence", d.MinConfidence); err != nil {
		return err
	}
	if d.Timeout != "" {
		t, err := time.ParseDuration(d.Timeout)
		if err != nil || t <= 0 || t > 10*time.Minute {
			return fmt.Errorf("parse config: decisions.timeout %q must be a duration between 0 and 10m, such as \"30s\"", d.Timeout)
		}
	}
	for _, point := range slices.Sorted(maps.Keys(d.Points)) {
		label := fmt.Sprintf("decisions.points.%q", point)
		if !decisionPoint.MatchString(point) {
			return fmt.Errorf("parse config: %s must be <package>.<point> (lowercase letters, digits, '-')", label)
		}
		pkg, _, _ := strings.Cut(point, ".")
		if _, ok := c.Frameworks[pkg]; !ok {
			return fmt.Errorf("parse config: %s belongs to package %q, which the config does not declare", label, pkg)
		}
		dp := d.Points[point]
		provider := cmpOr(dp.Provider, d.Default)
		if provider == "" {
			return fmt.Errorf("parse config: %s names no provider and [decisions] has no default", label)
		}
		if _, ok := d.Providers[provider]; !ok {
			return fmt.Errorf("parse config: %s provider %q names no [decisions.providers.%s]", label, provider, provider)
		}
		if err := unitInterval(label+" min_confidence", dp.MinConfidence); err != nil {
			return err
		}
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func unitInterval(label string, v *float64) error {
	if v != nil && !(*v >= 0 && *v <= 1) {
		return fmt.Errorf("parse config: %s must be in [0, 1]", label)
	}
	return nil
}

// decisionEndpoint requires https, except plain http to a loopback host
// (a local compatible service or a test double).
func decisionEndpoint(label, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("parse config: %s endpoint %q is not an absolute URL", label, raw)
	}
	if u.User != nil {
		return fmt.Errorf("parse config: %s endpoint must not carry credentials; use api_key_env", label)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("parse config: %s endpoint %q must use https (plain http only to localhost)", label, raw)
}

// DecisionRoute is the resolved provider configuration for one point.
type DecisionRoute struct {
	ProviderName  string
	Provider      DecisionProvider
	Endpoint      string
	MinConfidence *float64
	Timeout       time.Duration
}

// DecisionRouteFor resolves which provider answers point. The point need
// not be listed: unlisted points use [decisions].default.
func (c *Config) DecisionRouteFor(point string) (DecisionRoute, error) {
	d := c.Decisions
	dp := d.Points[point]
	name := cmpOr(dp.Provider, d.Default)
	if name == "" {
		return DecisionRoute{}, fmt.Errorf("%w (point %s)", decision.ErrNoProvider, point)
	}
	p := d.Providers[name]
	proto, _ := decision.LookupProtocol(p.Protocol)
	route := DecisionRoute{ProviderName: name, Provider: p, Endpoint: cmpOr(p.Endpoint, proto.DefaultEndpoint()), MinConfidence: d.MinConfidence, Timeout: DefaultDecisionTimeout}
	if dp.MinConfidence != nil {
		route.MinConfidence = dp.MinConfidence
	}
	if d.Timeout != "" {
		route.Timeout, _ = time.ParseDuration(d.Timeout)
	}
	return route, nil
}
