// Package buildinfo resolves the effective binary version. Release builds stamp
// the version via -ldflags "-X <pkg>.Version=...", but `go install <path>@<tag>`
// does not apply ldflags — so without a fallback such installs report the
// unstamped dev default instead of the tag the user asked for. Resolve fills that
// gap by reading the module version the Go toolchain embeds in the binary.
package buildinfo

import "runtime/debug"

// DevVersion is the unstamped default version every homonto-built binary is
// initialized with. Release builds override each CLI package's Version var via
// -ldflags "-X <pkg>.Version=..."; when unstamped (e.g. `go install ...@tag`,
// which applies no ldflags) Resolve recovers the module version.
const DevVersion = "0.1.0-dev"

// Resolve returns current unless it still equals dev — meaning release ldflags
// did not stamp it — in which case it returns the main module version recorded in
// the build info when that is a real tagged version. `go install <path>@v1.2.3`
// records v1.2.3 as Main.Version; a plain `go build` or `go install .` records
// "" or "(devel)", for which the dev default is kept.
func Resolve(current, dev string) string {
	if current != dev {
		return current
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return current
	}
	switch info.Main.Version {
	case "", "(devel)":
		return current
	default:
		return info.Main.Version
	}
}
