//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package worktask

import (
	"fmt"
	"os/exec"
)

func prepareCheckProcess(_ *exec.Cmd) error {
	return fmt.Errorf("integration checks require process-group cancellation support on this platform")
}

func stopCheckProcess(_ *exec.Cmd) {}
