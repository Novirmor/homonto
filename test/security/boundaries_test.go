// Package security pins the boundaries that are claimed in the
// documentation and are otherwise invisible in review.
//
// Each of these is a property of the WHOLE program, so no single package's
// tests can hold it. They are the kind of thing that erodes one innocuous
// import at a time.
package security

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// pkg is the part of `go list -json` this file reads.
type pkg struct {
	ImportPath string
	Imports    []string
	Deps       []string
	// Module is nil for the standard library and for the packages the
	// toolchain vendors into it, which is how those are told apart from
	// a real third-party dependency.
	Module *struct{ Path string }
}

// module is this module's path.
const module = "github.com/noviopenworks/homonto"

// ours reports whether an import path belongs to this module.
func ours(path string) bool {
	return path == module || strings.HasPrefix(path, module+"/")
}

// short trims the module prefix for readable failures.
func short(path string) string {
	return strings.TrimPrefix(path, module+"/")
}

// listPackages returns every package the shipped binary is built from.
// The binary's own dependency graph is the right question: a tool under
// tools/ may do whatever it likes, because it is not shipped.
func listPackages(t *testing.T) []pkg {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-json", "../..").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var packages []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, p)
	}
	return packages
}

// TestOnlyUpdateReachesTheNetwork is the documented promise that an
// ordinary Homonto process performs no network access at all.
//
// It is enforced structurally rather than by review: a telemetry call, a
// version check, a "helpful" fetch of a remote catalog would each be a
// small, reasonable-looking diff, and each would break the promise
// permanently. Only internal/update may import a network package, because
// only `homonto update` is allowed to use one.
func TestOnlyUpdateReachesTheNetwork(t *testing.T) {
	networked := map[string]bool{
		"net/http": true, "net": true, "net/url": false, // url parses; it does not dial
	}
	allowed := map[string]bool{
		"github.com/noviopenworks/homonto/internal/update": true,
	}
	var offenders []string
	for _, p := range listPackages(t) {
		if !ours(p.ImportPath) || allowed[p.ImportPath] {
			continue
		}
		for _, imported := range p.Imports {
			if networked[imported] {
				offenders = append(offenders, short(p.ImportPath)+" imports "+imported)
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("only internal/update may reach the network:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestNoShippedPackageImportsATestHelper catches the reverse of the usual
// mistake: a helper written for tests that quietly becomes production
// behavior, carrying its shortcuts with it.
func TestNoShippedPackageImportsATestHelper(t *testing.T) {
	for _, p := range listPackages(t) {
		if !ours(p.ImportPath) {
			continue
		}
		for _, imported := range p.Imports {
			if strings.HasPrefix(imported, "testing") ||
				strings.HasPrefix(imported, "github.com/noviopenworks/homonto/test/") {
				t.Errorf("%s imports %s", short(p.ImportPath), imported)
			}
		}
	}
}

// TestTheShippedBinaryCarriesOnlyTheExpectedModules. Every third-party
// module in the binary is code that runs with the user's file permissions
// on the user's source tree. Adding one should be a decision, not a
// side effect of reaching for a convenience.
func TestTheShippedBinaryCarriesOnlyTheExpectedModules(t *testing.T) {
	expected := map[string]bool{
		"github.com/spf13/cobra":           true,
		"github.com/spf13/pflag":           true, // cobra's own flag package
		"github.com/pelletier/go-toml/v2":  true,
		"golang.org/x/sys":                 true,
		"modernc.org/sqlite":               true,
		"modernc.org/libc":                 true,
		"modernc.org/mathutil":             true,
		"modernc.org/memory":               true,
		"github.com/dustin/go-humanize":    true,
		"github.com/google/uuid":           true,
		"github.com/ncruces/go-strftime":   true,
		"github.com/remyoudompheng/bigfft": true,
		"golang.org/x/exp":                 true,
	}
	var unexpected []string
	seen := map[string]bool{}
	for _, p := range listPackages(t) {
		if p.Module == nil || p.Module.Path == module || seen[p.Module.Path] {
			continue
		}
		seen[p.Module.Path] = true
		if !expected[p.Module.Path] {
			unexpected = append(unexpected, p.Module.Path)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Fatalf("the shipped binary carries modules nobody decided on:\n  %s\n"+
			"If one of these is intended, add it here deliberately.",
			strings.Join(unexpected, "\n  "))
	}
}
