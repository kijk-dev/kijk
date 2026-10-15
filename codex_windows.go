package main

import "syscall"

// WSAECONNREFUSED. syscall.ECONNREFUSED on Windows is an invented value that no call returns.
const errConnRefused = syscall.Errno(10061)
