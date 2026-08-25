//go:build linux || darwin

package verify

import (
	"os/exec"
	"syscall"
)

// processGroupSupported reports whether this platform can isolate and kill
// a whole process group.
const processGroupSupported = true

// isolate puts the command in its own process group, so a check's children
// are killable as a unit and cannot outlive the check that spawned them.
func isolate(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killGroup kills the command's whole process group with SIGKILL. A
// timeout is not a negotiation: a check that ignored its bound has already
// spent the time budget, and a graceful signal it may also ignore would
// only spend more. Killing the negated pid targets the group, so children
// die with the command.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// The group may already be gone (the process exited between the
		// timeout firing and the kill); fall back to the process itself.
		return cmd.Process.Kill()
	}
	return nil
}
