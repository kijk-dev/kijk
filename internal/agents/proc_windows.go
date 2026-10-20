package agents

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// processStart returns when the process started, in unix ms.
func processStart(pid int) (int64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	// An exited process stays openable while another program holds a handle to it.
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return 0, err
	}
	if code != stillActive {
		return 0, errors.New("process has exited")
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return created.Nanoseconds() / 1e6, nil
}
