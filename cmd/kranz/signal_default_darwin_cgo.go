//go:build darwin && cgo

package main

/*
#include <errno.h>
#include <signal.h>
#include <unistd.h>

static int kranz_signal_default_and_raise(int sig) {
	struct sigaction action;
	if (sigemptyset(&action.sa_mask) != 0) return errno;
	action.sa_handler = SIG_DFL;
	action.sa_flags = 0;
	if (sigaction(sig, &action, NULL) != 0) return errno;
	if (kill(getpid(), sig) != 0) return errno;
	return 0;
}
*/
import "C"

import "syscall"

func reraiseDefaultSignal(sig syscall.Signal) error {
	if errno := C.kranz_signal_default_and_raise(C.int(sig)); errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}
