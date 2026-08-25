package registration

import (
	"fmt"
	"os"
	"path/filepath"
)

// StateRootEnv overrides where a non-Git member's registration and lease
// slot lives.
//
// Non-Git members have nowhere beside themselves to keep a slot — a plain
// directory has no .git to hide one in — so the slot goes in the machine's
// state directory, which by default is under the user's home. That is
// right for a person's installation and wrong everywhere else: a test
// suite, a container, or a CI runner would write into whoever's home the
// process happens to have, and leave it there.
//
// Setting this to a directory redirects every slot into it.
const StateRootEnv = "HOMONTO_STATE_ROOT"

// StateRoot returns the state BASE the slot functions append to: the
// override when set, the platform default otherwise. The homonto
// component is appended by the slot functions; do not pre-append it.
func StateRoot() (string, error) {
	if override := os.Getenv(StateRootEnv); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("registration: %s must be an absolute path, got %q",
				StateRootEnv, override)
		}
		return override, nil
	}
	return DefaultStateRoot()
}
