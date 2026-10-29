package git

import (
	"errors"

	"golang.org/x/sys/windows"
)

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED) // another user's process
	}
	defer windows.CloseHandle(h)
	// An exited process stays openable while another program holds a handle to it.
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}
