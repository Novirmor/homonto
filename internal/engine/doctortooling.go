package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/config"
)

// toolingProbe describes how to detect one provider. Detection is LookPath and
// directory existence ONLY — nothing is executed. Running a provider to see if
// it works would put an unbounded external process on the doctor path, which is
// exactly what the v0.7.0 exec-timeout hardening exists to avoid.
type toolingProbe struct {
	// binaries are candidate executable names; any one on PATH counts.
	binaries []string
	// markers are project-root-relative directories; any one present counts.
	// An index or bundle is what grounding actually reads, so it counts as
	// present even when the generator itself is not installed.
	markers []string
}

var toolingProbes = map[string]toolingProbe{
	"rtk":      {binaries: []string{"rtk"}},
	"graphify": {binaries: []string{"graphify"}, markers: []string{"graphify-out", ".codegraph"}},
	"okf":      {binaries: []string{"okf_lookup.py", "okf"}, markers: []string{".okf", "okf-out"}},
}

// toolingFindings reports one advisory line per DECLARED provider. An
// undeclared slot ("none") produces nothing: there is no such thing as a
// missing provider the user never asked for.
//
// lookPath is injected so tests do not depend on what happens to be installed
// on the machine running them.
func toolingFindings(t config.Tooling, projectRoot string, lookPath func(string) (string, error)) []string {
	var out []string
	for _, slot := range []struct{ key, provider string }{
		{"shell_proxy", t.ShellProxy},
		{"code_intel", t.CodeIntel},
	} {
		if slot.provider == "" || slot.provider == config.ToolingNone {
			continue
		}
		probe, known := toolingProbes[slot.provider]
		if !known {
			// Config validation rejects unknown providers, so this is a
			// belt-and-braces line rather than a reachable user error.
			out = append(out, fmt.Sprintf("warn: tooling.%s declares unknown provider %q", slot.key, slot.provider))
			continue
		}
		if toolingProviderPresent(probe, projectRoot, lookPath) {
			out = append(out, fmt.Sprintf("ok: tooling.%s provider %s detected", slot.key, slot.provider))
			continue
		}
		out = append(out, fmt.Sprintf(
			"warn: tooling.%s declares %q but it was not detected — the workflow will warn and fall back; install it or set the key to \"none\"",
			slot.key, slot.provider))
	}
	return out
}

func toolingProviderPresent(p toolingProbe, projectRoot string, lookPath func(string) (string, error)) bool {
	for _, b := range p.binaries {
		if _, err := lookPath(b); err == nil {
			return true
		}
	}
	for _, m := range p.markers {
		if fi, err := os.Stat(filepath.Join(projectRoot, m)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// doctorTooling is the Engine-bound wrapper Doctor calls.
func (e *Engine) doctorTooling() []string {
	return toolingFindings(e.Cfg.ResolvedTooling(), e.ProjectRoot, exec.LookPath)
}
