//go:build darwin || freebsd || netbsd || openbsd

package tui

import "syscall"

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
