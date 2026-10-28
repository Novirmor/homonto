//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package worktask

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// A check commonly starts a shell or build tool with children. Killing only
// that parent would let its children write after participating writers resume.
func prepareCheckProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// stopCheckProcess kills descendants left in the check's process group after
// its leader exited; the group outlives its leader while members remain.
func stopCheckProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
