package agents

import "golang.org/x/sys/unix"

// processStart returns when the process started, in unix ms.
func processStart(pid int) (int64, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return k.Proc.P_starttime.Nano() / 1e6, nil
}
