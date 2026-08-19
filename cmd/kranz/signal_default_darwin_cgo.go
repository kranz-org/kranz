//go:build darwin && cgo

package main

/*
#include <errno.h>
#include <signal.h>

static int kranz_signal_default(int sig) {
	struct sigaction action;
	if (sigemptyset(&action.sa_mask) != 0) return errno;
	action.sa_handler = SIG_DFL;
	action.sa_flags = 0;
	if (sigaction(sig, &action, NULL) != 0) return errno;
	return 0;
}
*/
import "C"

import "syscall"

func forceDefaultSignal(sig syscall.Signal) error {
	if errno := C.kranz_signal_default(C.int(sig)); errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}
