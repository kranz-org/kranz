//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

type kernelSignalAction struct {
	handler  uintptr
	flags    uint64
	restorer uintptr
	mask     uint64
}

func reraiseDefaultSignal(sig syscall.Signal) error {
	action := kernelSignalAction{}
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&action)), 0, 8, 0, 0)
	if errno != 0 {
		return errno
	}
	return syscall.Kill(syscall.Getpid(), sig)
}
