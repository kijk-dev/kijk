//go:build !windows

package main

import (
	"os"
	"syscall"
)

const canRestart = true

// restartInPlace replaces this process with exe: same process ID, terminal and Ctrl+C. Go opens
// every file close-on-exec, so the listening socket closes and the new kijk can bind the port.
func restartInPlace(exe string, args []string, token string) error {
	env := append(os.Environ(), "KIJK_TOKEN="+token, "KIJK_RESTARTED=1")
	return syscall.Exec(exe, append([]string{exe}, args...), env)
}
