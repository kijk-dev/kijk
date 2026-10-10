//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts the browser in its own process group, so Ctrl+C on kijk does not close it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
