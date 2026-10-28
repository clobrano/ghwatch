//go:build !linux

package main

import (
	"errors"
	"net"
)

// peerPID is only implemented on Linux.
func peerPID(*net.UnixConn) (int, error) {
	return 0, errors.New("finding the daemon is not supported on this platform")
}
