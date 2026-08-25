//go:build !linux && !darwin

package verify

import "os/exec"

// processGroupSupported is false where process groups are unavailable:
// a timed-out check can only be killed as a single process, and its
// children may survive it. Runner records that in the result's error.
const processGroupSupported = false

// isolate is a no-op on platforms without process groups.
func isolate(cmd *exec.Cmd) {}

// killGroup kills only the command itself.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
