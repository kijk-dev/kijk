//go:build !windows

package main

import "syscall"

const errConnRefused = syscall.ECONNREFUSED
