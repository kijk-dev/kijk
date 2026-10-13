package main

import "errors"

// lockFile is only used by kijk serve, which does not run on Windows.
func lockFile(path string) (func(), error) {
	return nil, errors.New("not supported on Windows")
}
