//go:build !windows

package main

import (
	"io/fs"
	"syscall"
)

// fileOwner gives the uid that owns a file, or -1.
func fileOwner(st fs.FileInfo) int {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid)
	}
	return -1
}
