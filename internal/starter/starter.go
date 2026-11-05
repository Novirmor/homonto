// Package starter declares the ready-made starting configurations homonto
// init can generate (M5.1, ADR 0110): one host per hosting workflow, the
// harnesses with a workflow-tier adapter, and the builtin engines. Every
// combination of them is a supported cell, each one is planned and applied in
// tests, and the 1.0 release gate keys its evidence to the same cells.
package starter

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Host is one supported hosting service and the workflow that serves it.
type Host struct {
	ID       string // the service id: github, gitlab, bitbucket
	Name     string // the product name, for messages and docs
	Workflow string // the builtin hosting workflow package
	Auth     string // how its credentials are provided, outside homonto.toml
}

// Hosts are the hosts init can configure, in display order.
var Hosts = []Host{
	{"github", "GitHub", "h-workflow-gh", "gh auth login"},
	{"gitlab", "GitLab.com", "h-workflow-gl", "glab auth login"},
	{"bitbucket", "Bitbucket Cloud", "h-workflow-bb", "an API token in HOMONTO_BITBUCKET_TOKEN"},
}

// Harness is one supported agent harness.
type Harness struct {
	ID   string
	Name string
	// ModelHint names the model spelling the harness takes.
	ModelHint string
}

// Harnesses are the harnesses init can configure, in display order.
var Harnesses = []Harness{
	{"opencode", "OpenCode 2", "provider/model, for example anthropic/claude-sonnet-5-5"},
	{"claude", "Claude Code", "an alias (sonnet, opus, haiku) or a model id"},
}

// Engines are the builtin engines init can configure, in display order.
var Engines = []string{"onto", "to", "no"}

// Selection is what the user chose.
type Selection struct {
	Hosts     []string
	Harnesses []string
	Engines   []string
	// Models maps a harness id to the model every agent on it runs on. It
	// becomes that harness's [routing.default] policy.
	Models map[string]string
	// Records is the workflow records root; empty means the default (docs).
	Records string
	// Tmp declares the [tmp] scratch directory.
	Tmp bool
	// Repos are sibling repositories, in order: alias and path.
	Repos [][2]string
}

// ErrUnsupported marks a selection outside the starter matrix.
var ErrUnsupported = errors.New("unsupported starting configuration")

func hostIDs() []string {
	out := make([]string, len(Hosts))
	for i, h := range Hosts {
		out[i] = h.ID
	}
	return out
}

func harnessIDs() []string {
	out := make([]string, len(Harnesses))
	for i, h := range Harnesses {
		out[i] = h.ID
	}
	return out
}

func host(id string) Host {
	for _, h := range Hosts {
		if h.ID == id {
			return h
		}
	}
	return Host{}
}

var repoAlias = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Validate refuses anything outside the matrix before a file is written.
func (s Selection) Validate() error {
	check := func(kind string, got, valid []string) error {
		seen := map[string]bool{}
		for _, v := range got {
			if !slices.Contains(valid, v) {
				return fmt.Errorf("%w: %s %q is not one of %s", ErrUnsupported, kind, v, strings.Join(valid, ", "))
			}
			if seen[v] {
				return fmt.Errorf("%s %q is selected twice", kind, v)
			}
			seen[v] = true
		}
		return nil
	}
	if len(s.Harnesses) == 0 {
		return fmt.Errorf("select at least one harness (--harness %s)", strings.Join(harnessIDs(), "|"))
	}
	if err := check("harness", s.Harnesses, harnessIDs()); err != nil {
		return err
	}
	if err := check("host", s.Hosts, hostIDs()); err != nil {
		return err
	}
	if err := check("engine", s.Engines, Engines); err != nil {
		return err
	}
	if len(s.Hosts) == 0 && len(s.Engines) == 0 {
		return errors.New("select at least one engine (--engine onto|to|no) or host (--host github|gitlab|bitbucket); with neither, run homonto init without selections for the commented scaffold")
	}
	for h := range s.Models {
		if !slices.Contains(s.Harnesses, h) {
			return fmt.Errorf("--model %s=… names a harness that is not selected; add --harness %s or drop the model", h, h)
		}
	}
	for _, h := range s.Harnesses {
		if strings.TrimSpace(s.Models[h]) == "" {
			return fmt.Errorf("--model %s=<model> is required: every agent needs a model, and homonto never picks one for you (%s takes %s)", h, harnessName(h), harnessHint(h))
		}
	}
	seen := map[string]bool{}
	for _, r := range s.Repos {
		if !repoAlias.MatchString(r[0]) || strings.Trim(r[0], "0123456789") == "" {
			return fmt.Errorf("--repo %s=…: an alias uses letters, digits, '-' or '_' and is not numeric-only", r[0])
		}
		if seen[r[0]] {
			return fmt.Errorf("--repo %s is selected twice", r[0])
		}
		seen[r[0]] = true
		if r[1] == "" {
			return fmt.Errorf("--repo %s= has no path", r[0])
		}
	}
	return nil
}

func harnessName(id string) string {
	for _, h := range Harnesses {
		if h.ID == id {
			return h.Name
		}
	}
	return id
}

func harnessHint(id string) string {
	for _, h := range Harnesses {
		if h.ID == id {
			return h.ModelHint
		}
	}
	return ""
}

// ordered returns sel's members in the order of all.
func ordered(sel, all []string) []string {
	var out []string
	for _, v := range all {
		if slices.Contains(sel, v) {
			out = append(out, v)
		}
	}
	return out
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// Render produces the homonto.toml for a valid selection. It writes no
// credentials: each host authenticates through its own CLI or environment.
func Render(s Selection) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	harnesses := ordered(s.Harnesses, harnessIDs())
	hosts := ordered(s.Hosts, hostIDs())
	engines := ordered(s.Engines, Engines)
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	summary := []string{"harness " + strings.Join(harnesses, " + ")}
	if len(hosts) > 0 {
		summary = append(summary, "host "+strings.Join(hosts, " + "))
	}
	if len(engines) > 0 {
		summary = append(summary, "engine "+strings.Join(engines, " + "))
	}
	line("# Generated by homonto init (%s).", strings.Join(summary, ", "))
	line("# Review it, then run homonto plan and homonto apply. It holds no")
	line("# credentials: they stay with your harness and host CLIs.")
	line("# Reference: docs/guides/configuration.md")
	line("")
	quoted := make([]string, len(harnesses))
	for i, h := range harnesses {
		quoted[i] = quote(h)
	}
	line("harnesses = [%s]", strings.Join(quoted, ", "))
	line("")
	line("[workflow]")
	records := s.Records
	if records == "" {
		records = "docs"
	}
	line("root = %s", quote(records))
	for _, e := range engines {
		line("")
		line("[engines.%s]", e)
		line(`source = "builtin:%s"`, e)
		line(`scope = "project"`)
	}
	for _, id := range hosts {
		h := host(id)
		line("")
		line("# %s: authenticate with %s.", h.Name, h.Auth)
		line("[workflows.%s]", h.Workflow)
		line(`source = "builtin:%s"`, h.Workflow)
		line(`scope = "project"`)
	}
	line("")
	line("# Every agent runs on this model unless a narrower policy or a")
	line("# [subagents.<name>.<harness>] pin says otherwise (docs/contracts/routing.md).")
	for _, h := range harnesses {
		line("[routing.default.%s]", h)
		line("model = %s", quote(s.Models[h]))
		line("")
	}
	if s.Tmp {
		line("# Scratch space every agent can write without prompts.")
		line("[tmp]")
		line(`dir = ".tmp"`)
		line("")
	}
	if len(s.Repos) > 0 {
		line("# Sibling repositories; this config's own repository is implicit.")
		line("[repos]")
		for _, r := range s.Repos {
			line("%s = %s", r[0], quote(r[1]))
		}
		line("")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// Cells enumerates every supported host × harness pairing, each with every
// engine selected: the pairings the onboarding acceptance path and the
// release gate cover. "" is the no-host cell.
func Cells() [][2]string {
	var out [][2]string
	for _, h := range append([]string{""}, hostIDs()...) {
		for _, harness := range harnessIDs() {
			out = append(out, [2]string{h, harness})
		}
	}
	return out
}

// Evidence is what a release must show for one dimension of a cell before
// it may be claimed (ADR 0110, docs/release-checklist.md).
type Evidence struct {
	ID    string // stable evidence key, used in the release evidence manifest
	Check string // the command or suite that produces it
}

// AllCells evidence: every cell is generated, planned, applied and taken to
// its first change and task.
var AllCells = []Evidence{
	{"unit/starter", "go test ./internal/engine -run TestEveryStarterSelectionPlansAndApplies"},
	{"install/first-task", "scripts/install-test.sh (t40, one run per host × harness)"},
}

// HarnessEvidence are the real-harness matrix cells each harness needs.
var HarnessEvidence = map[string][]Evidence{
	"opencode": {
		{"e2e/projection/opencode", "scripts/e2e-matrix.sh projection/opencode"},
		{"e2e/live/opencode", "scripts/e2e-matrix.sh live/opencode"},
		{"e2e/recovery/opencode", "scripts/e2e-matrix.sh recovery/opencode"},
		{"e2e/onto", "scripts/e2e-matrix.sh onto"},
		{"e2e/reference", "scripts/e2e-matrix.sh reference"},
	},
	"claude": {
		{"e2e/projection/claude", "scripts/e2e-matrix.sh projection/claude"},
		{"e2e/recovery/claude", "scripts/e2e-matrix.sh recovery/claude"},
		{"e2e/policy/claude", "scripts/e2e-matrix.sh policy/claude"},
		{"e2e/reference", "scripts/e2e-matrix.sh reference"},
	},
}

// HostEvidence are the hosting sandbox runs each host needs.
var HostEvidence = map[string][]Evidence{
	"github":    {{"sandbox/github", "scripts/hosting-sandbox.sh github (runtime and process layers)"}},
	"gitlab":    {{"sandbox/gitlab", "scripts/hosting-sandbox.sh gitlab (runtime and process layers)"}},
	"bitbucket": {{"sandbox/bitbucket", "scripts/hosting-sandbox.sh bitbucket (runtime and process layers)"}},
}

// CellEvidence lists every evidence key one host × harness cell needs.
func CellEvidence(host, harness string) []Evidence {
	out := append([]Evidence(nil), AllCells...)
	out = append(out, HarnessEvidence[harness]...)
	out = append(out, HostEvidence[host]...)
	return out
}
