//go:build darwin && !cgo

package main

import (
	"syscall"
	"unsafe"
)

type userSignalAction struct {
	handler uintptr
	tramp   uintptr
	mask    uint32
	flags   int32
}

func reraiseDefaultSignal(sig syscall.Signal) error {
	action := userSignalAction{}
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&action)), 0)
	if errno != 0 {
		return errno
	}
	return syscall.Kill(syscall.Getpid(), sig)
}
