//go:build !darwin && !linux

package main

import (
	"os"
	"syscall"
)

func reraiseDefaultSignal(sig syscall.Signal) error { return syscall.Kill(os.Getpid(), sig) }
