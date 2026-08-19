//go:build !darwin && !linux

package main

import "syscall"

func forceDefaultSignal(syscall.Signal) error { return nil }
