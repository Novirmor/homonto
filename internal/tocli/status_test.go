package tocli

import (
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/tostate"
)

// TestStatus_TextOutputShape verifies the text (non-JSON) rendering of status:
// "name\tphase" per line, and the invalid-entry shape surfaces the underlying
// error inline.
func TestStatus_TextOutputShape(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "alpha", "--dir", dir)
	run(t, false, "new", "beta", "--dir", dir)
	run(t, false, "phase", "beta", "--dir", dir)

	out := run(t, false, "status", "--dir", dir)
	if !strings.Contains(out, "alpha\tplan\n") {
		t.Errorf("status text %q missing an 'alpha\\tplan' line", out)
	}
	if !strings.Contains(out, "beta\tdo\n") {
		t.Errorf("status text %q missing a 'beta\\tdo' line", out)
	}
}

// TestStatus_TextOutputInvalidEntry verifies an invalid entry in text mode is
// reported as "name\tinvalid\t<error>" rather than aborting the listing.
func TestStatus_TextOutputInvalidEntry(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "ok", "--dir", dir)
	writeFile(t, statePath(dir, "broken"), "phase: nonsense\n")
	out := run(t, false, "status", "--dir", dir)
	if !strings.Contains(out, "broken\tinvalid\t") {
		t.Errorf("status text %q missing a 'broken\\tinvalid\\t…' line", out)
	}
}

// TestStatus_EntryCarriesCreatedAndVerified verifies the JSON entry surface
// for a done-flag-but-active change carries the Verified flag.
func TestStatus_EntryCarriesCreatedAndVerified(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "v", "--dir", dir)
	if err := tostate.Save(statePath(dir, "v"), tostate.State{
		Change: "v", Phase: tostate.PhaseDo, Created: "2030-09-09", Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	out := run(t, false, "status", "--json", "--dir", dir)
	for _, want := range []string{`"verified": true`, `"created": "2030-09-09"`, `"phase": "do"`} {
		if !strings.Contains(out, want) {
			t.Errorf("status JSON %q missing %s", out, want)
		}
	}
}
