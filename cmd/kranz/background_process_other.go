//go:build !darwin && !linux

package main

import "syscall"

func backgroundProcessAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
