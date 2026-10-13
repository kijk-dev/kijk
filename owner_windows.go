package main

import "io/fs"

// fileOwner has no uid to give on Windows, where remote machines are not supported.
func fileOwner(fs.FileInfo) int { return -1 }
