package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/clobrano/ghwatch/internal/config"
)

// cmdStop stops the running daemon, whichever build it is from: it asks
// the socket which process listens on it and sends it SIGTERM, so it
// needs no command the daemon may not know.
func cmdStop(paths config.Paths, out io.Writer) error {
	conn, err := net.Dial("unix", paths.Socket())
	if err != nil {
		fmt.Fprintln(out, "no daemon running")
		return nil
	}
	pid, err := peerPID(conn.(*net.UnixConn))
	conn.Close()
	if err != nil {
		return fmt.Errorf("stop: %w; use: pkill -x ghwatch", err)
	}
	if pid <= 0 || pid == syscall.Getpid() {
		return fmt.Errorf("stop: unexpected daemon pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop daemon (pid %d): %w", pid, err)
	}
	// The daemon removes its socket on the way out.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("unix", paths.Socket()); err != nil {
			fmt.Fprintf(out, "stopped the daemon (pid %d)\n", pid)
			return nil
		} else {
			c.Close()
		}
	}
	return errors.New("stop: the daemon is still running after 5s")
}
