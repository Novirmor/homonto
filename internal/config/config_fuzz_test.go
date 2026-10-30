package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad feeds arbitrary bytes to the config loader (parse + validate). Load
// must never panic — malformed or hostile input must always surface as an error,
// never a crash, since the config is user-authored and the first thing every
// command touches.
func FuzzLoad(f *testing.F) {
	f.Add("[settings.opencode]\nshare = \"manual\"\n")
	f.Add("[skills.s]\nsource = \"local:s\"\nscope = \"user\"\n")
	f.Add("[agents.a]\nsource = \"builtin:code-reviewer\"\n")
	f.Add("[plugins.claude.p]\nsource = \"p@m\"\n")
	f.Add("not toml at all \x00\xff")
	f.Add("")

	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, toml string) {
		p := filepath.Join(dir, "homonto.toml")
		if err := os.WriteFile(p, []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
		// The only contract under fuzz: never panic. Any input is either a valid
		// config or an error.
		_, _ = Load(p)
	})
}
