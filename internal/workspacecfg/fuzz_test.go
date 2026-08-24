package workspacecfg

import (
	"errors"
	"strings"
	"testing"
)

// FuzzDecode feeds arbitrary bytes to the strict decoder. The contract: never
// panic, and every error is one of the package's typed errors — the workspace
// manifest is the first user-authored thing every command touches.
func FuzzDecode(f *testing.F) {
	f.Add("schema_version = 1\n[workspace]\nid = \"0a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d\"\nworkflow = \"task\"\n[control]\nid = \"11223344-5566-4777-8888-99aabbccdde0\"\npath = \".\"\n")
	f.Add("schema_version = 1\n[[members]]\nid = \"11223344-5566-4777-8888-99aabbccdde0\"\npath = \".\"\nkind = \"git\"\n[[members.verification]]\nname = \"u\"\ncommand = [\"go\", \"test\"]\ntimeout = \"5m\"\n[members.paths]\ntests = [\"**/*_test.go\"]\n")
	f.Add("schema_version = 2\n")
	f.Add("schema_version = 1\nbogus = 1\n")
	f.Add("schema_version = 1\n[workspace]\nworkflow = 3\n")
	f.Add("not toml at all \x00\xff")
	f.Add("")
	f.Fuzz(func(t *testing.T, doc string) {
		cfg, err := Decode(strings.NewReader(doc))
		if err != nil {
			typed := errors.Is(err, ErrInvalidTOML) ||
				errors.Is(err, ErrUnknownField) ||
				errors.Is(err, ErrMissingSchemaVersion) ||
				errors.Is(err, ErrUnsupportedSchema)
			if !typed {
				t.Fatalf("Decode returned untyped error: %v", err)
			}
			return
		}
		if _, err := Marshal(cfg); err != nil {
			t.Fatalf("Marshal(Decode(...)) failed on accepted input: %v", err)
		}
		_ = MembershipFingerprint(cfg)
	})
}
