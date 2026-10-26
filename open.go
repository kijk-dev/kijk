package main

// openDesk is the server side of every hand-off from the command line.
type openDesk struct {
	h  *hub
	rl *rail
	fl *folderList
}
