//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package swarm

import "syscall"

func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
