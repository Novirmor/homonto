// Package opid supplies operation identity: the IDs and timestamps that name
// individual applies, evidence records, handoff packs, promotion staging, and
// snapshot journals. Identity is injectable so tests are deterministic; a
// no-op operation never allocates one (callers decide what counts as an
// operation — the package only guarantees uniqueness and order capture).
package opid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Clock returns the time an operation happened. Injected for tests.
type Clock func() time.Time

// IDGen returns a new unique operation ID. Injected for tests.
type IDGen func() string

// Supplier bundles the two injectables. The zero value is not usable; use
// New.
type Supplier struct {
	NewID IDGen
	Now   Clock
}

// New returns the production supplier: 8-hex random IDs (the same shape as
// onto's stable change IDs) and UTC wall time.
func New() Supplier {
	return Supplier{
		NewID: func() string {
			b := make([]byte, 4)
			if _, err := rand.Read(b); err != nil {
				// crypto/rand failure is catastrophic and unrecoverable;
				// a timestamp-derived fallback at least stays unique within
				// a process and never silently repeats.
				return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
			}
			return hex.EncodeToString(b)
		},
		Now: func() time.Time { return time.Now().UTC() },
	}
}

// Fixed returns a fully deterministic supplier for tests: sequential IDs
// ("op-000001"...) from a counter and a frozen time.
func Fixed(t time.Time) Supplier {
	n := 0
	return Supplier{
		NewID: func() string {
			n++
			return fmt.Sprintf("op-%06d", n)
		},
		Now: func() time.Time { return t.UTC() },
	}
}
