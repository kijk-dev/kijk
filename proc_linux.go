package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processStart returns when the process started, in unix ms.
func processStart(pid int) (int64, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// The command name in field 2 can hold spaces and ")", so count fields from its last ")".
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, fmt.Errorf("cannot parse /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 {
		return 0, fmt.Errorf("cannot parse /proc/%d/stat", pid)
	}
	// Field 22: clock ticks after boot. Linux reports 100 ticks a second to every program.
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, err
	}
	boot, err := bootTime()
	if err != nil {
		return 0, err
	}
	return boot*1000 + ticks*10, nil
}

// bootTime returns when the machine booted, in unix seconds.
func bootTime() (int64, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, errors.New("no btime in /proc/stat")
}
