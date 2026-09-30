//go:build unix

package tui

import (
	"errors"
	"syscall"
	"unsafe"
)

type termState struct{ t syscall.Termios }

func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// makeRaw puts the terminal in raw mode and returns the previous state.
func makeRaw(fd int) (*termState, error) {
	var old syscall.Termios
	if err := ioctl(fd, ioctlGetTermios, unsafe.Pointer(&old)); err != nil {
		return nil, errors.New("not a terminal")
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, ioctlSetTermios, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return &termState{t: old}, nil
}

func restore(fd int, s *termState) error {
	return ioctl(fd, ioctlSetTermios, unsafe.Pointer(&s.t))
}

// termSize returns the terminal's columns and rows.
func termSize(fd int) (int, int, error) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}
