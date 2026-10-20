//go:build !windows

package agents

import "syscall"

const errConnRefused = syscall.ECONNREFUSED
