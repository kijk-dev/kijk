package main

import "errors"

// Windows cannot replace a running process in place, so the status bar asks for a manual restart.
const canRestart = false

func restartInPlace(exe string, args []string, token string) error {
	return errors.New("Windows cannot restart kijk in place")
}
