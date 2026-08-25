// Package verify executes a workspace's configured verification commands
// and records what they proved. Homonto runs the checks itself rather than
// believing an agent's claim, so everything here is about making a run
// reproducible and its evidence honest.
//
// # Execution
//
// Commands are explicit argument vectors — never shell strings — run from
// a configured directory inside the member, under a bounded timeout, in
// their own process group, with an environment built ONLY from the names
// the check allowlists. Nothing ambient leaks in: if a command needs PATH,
// the check must say so, and a bare argv[0] is resolved against the
// allowlisted PATH or refused. A check that outlives its timeout has its
// whole process group killed, so a spawned child cannot keep running after
// the check that started it is over.
//
// # Evidence
//
// A result carries two layers. The raw streams are LOCAL: they stay in the
// runtime database, redacted, and never travel. The portable summary is
// content-free — byte and line counts plus a digest of the redacted
// output — so a checkpoint can prove which output was seen without
// carrying a word of it.
//
// Freshness is a comparison, not a timestamp: a result set is fresh only
// while every input it was taken against still matches (see Fresh).
package verify

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// DefaultTimeout is the bound applied to a check that configures none. It
// is the manifest's documented default, parsed once.
var DefaultTimeout = mustParseDefaultTimeout()

func mustParseDefaultTimeout() time.Duration {
	d, err := time.ParseDuration(workspacecfg.DefaultCheckTimeout)
	if err != nil {
		panic(fmt.Sprintf("verify: default check timeout %q: %v", workspacecfg.DefaultCheckTimeout, err))
	}
	return d
}

// Spec is one check ready to run: the resolved argv, the member-relative
// directory it runs in, the environment NAMES whose values are forwarded,
// and the timeout that bounds it.
type Spec struct {
	Name        string        `json:"name"`
	Command     []string      `json:"command"`
	WorkingDir  string        `json:"working_dir,omitempty"`
	Environment []string      `json:"environment,omitempty"`
	Timeout     time.Duration `json:"timeout"`
}

// FromConfig converts one configured check into a Spec, applying the
// default timeout and rejecting what the manifest schema would reject —
// this package never trusts a Config it did not validate itself.
func FromConfig(c workspacecfg.Check) (Spec, error) {
	if c.Name == "" {
		return Spec{}, fmt.Errorf("verify: check name must not be empty")
	}
	if len(c.Command) == 0 {
		return Spec{}, fmt.Errorf("verify: check %q: command must be a non-empty argv array", c.Name)
	}
	for i, arg := range c.Command {
		if arg == "" {
			return Spec{}, fmt.Errorf("verify: check %q: command[%d] must not be empty", c.Name, i)
		}
	}
	timeout := DefaultTimeout
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return Spec{}, fmt.Errorf("verify: check %q: timeout %q: %w", c.Name, c.Timeout, err)
		}
		if d <= 0 {
			return Spec{}, fmt.Errorf("verify: check %q: timeout %q must be positive", c.Name, c.Timeout)
		}
		timeout = d
	}
	for _, name := range c.Environment {
		if err := validateEnvName(name); err != nil {
			return Spec{}, fmt.Errorf("verify: check %q: %w", c.Name, err)
		}
	}
	return Spec{
		Name:        c.Name,
		Command:     append([]string(nil), c.Command...),
		WorkingDir:  c.WorkingDir,
		Environment: append([]string(nil), c.Environment...),
		Timeout:     timeout,
	}, nil
}

// SpecsFor returns the checks configured for one member of a workspace.
func SpecsFor(cfg workspacecfg.Config, repo identity.RepositoryID) ([]Spec, error) {
	for _, m := range cfg.Members {
		if m.ID != repo {
			continue
		}
		specs := make([]Spec, 0, len(m.Verification))
		for _, c := range m.Verification {
			spec, err := FromConfig(c)
			if err != nil {
				return nil, err
			}
			specs = append(specs, spec)
		}
		return specs, nil
	}
	return nil, fmt.Errorf("verify: no member %s in the workspace", repo)
}

// Digest fingerprints the spec exactly as it will run. It is what a
// check-configuration change moves, and therefore what makes a recorded
// result stale.
func (s Spec) Digest() (fingerprint.Digest, error) {
	return fingerprint.CanonicalJSON("verify-spec", s)
}

// validateEnvName enforces the manifest's NAMES-ONLY rule. Values are
// never configured — only the names whose ambient values are forwarded.
func validateEnvName(name string) error {
	if name == "" {
		return fmt.Errorf("environment name must not be empty")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return fmt.Errorf("environment name %q is not letters, digits, and underscores not starting with a digit", name)
		}
	}
	return nil
}

// Inputs are everything a result set is taken against. Two sets are
// comparable only when their inputs are: change any of these and the
// evidence describes a world that no longer exists.
type Inputs struct {
	// Repository is the member the checks ran against.
	Repository identity.RepositoryID `json:"repository"`
	// Config is workspacecfg.VerificationFingerprint for that member.
	Config fingerprint.Digest `json:"config"`
	// Sources are the integrated source fingerprints the checks saw.
	Sources []fingerprint.Digest `json:"sources"`
	// Artifacts are the document digests the checks assert about.
	Artifacts []fingerprint.Digest `json:"artifacts"`
}

// InputsFor builds the configuration half of Inputs from a manifest.
func InputsFor(cfg workspacecfg.Config, repo identity.RepositoryID) (Inputs, error) {
	digest, err := workspacecfg.VerificationFingerprint(cfg, repo)
	if err != nil {
		return Inputs{}, err
	}
	return Inputs{Repository: repo, Config: digest}, nil
}

// Digest fingerprints the whole input set.
func (in Inputs) Digest() (fingerprint.Digest, error) {
	return fingerprint.CanonicalJSON("verify-inputs", in.canonical())
}

// canonical returns the inputs with digest lists sorted and deduplicated,
// so equal input sets always compare and digest equal regardless of the
// order the caller collected them in.
func (in Inputs) canonical() Inputs {
	return Inputs{
		Repository: in.Repository,
		Config:     in.Config,
		Sources:    sortedUnique(in.Sources),
		Artifacts:  sortedUnique(in.Artifacts),
	}
}

// Validate checks that the inputs are usable evidence anchors.
func (in Inputs) Validate() error {
	if err := identity.ValidateUUID(string(in.Repository)); err != nil {
		return fmt.Errorf("verify: inputs.repository: %w", err)
	}
	if err := in.Config.Validate(); err != nil {
		return fmt.Errorf("verify: inputs.config: %w", err)
	}
	for i, d := range in.Sources {
		if err := d.Validate(); err != nil {
			return fmt.Errorf("verify: inputs.sources[%d]: %w", i, err)
		}
	}
	for i, d := range in.Artifacts {
		if err := d.Validate(); err != nil {
			return fmt.Errorf("verify: inputs.artifacts[%d]: %w", i, err)
		}
	}
	return nil
}

// MarshalInputs encodes inputs canonically for storage.
func MarshalInputs(in Inputs) ([]byte, error) {
	b, err := json.Marshal(in.canonical())
	if err != nil {
		return nil, fmt.Errorf("verify: encode inputs: %w", err)
	}
	return b, nil
}

// UnmarshalInputs decodes stored inputs.
func UnmarshalInputs(b []byte) (Inputs, error) {
	var in Inputs
	if err := json.Unmarshal(b, &in); err != nil {
		return Inputs{}, fmt.Errorf("verify: decode inputs: %w", err)
	}
	return in, nil
}
