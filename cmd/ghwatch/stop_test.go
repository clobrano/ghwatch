package main

import (
	"bytes"
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/clobrano/ghwatch/internal/config"
)

func TestStopWithoutDaemon(t *testing.T) {
	rt, err := os.MkdirTemp("", "gw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(rt)
	var out bytes.Buffer
	if err := cmdStop(config.Paths{RuntimeDir: rt}, &out); err != nil || out.String() != "no daemon running\n" {
		t.Errorf("stop = %q, %v", out.String(), err)
	}
}

func TestPeerPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	rt, err := os.MkdirTemp("", "gw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(rt)
	sock := rt + "/s.sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The listener is this process.
	if pid, err := peerPID(c.(*net.UnixConn)); err != nil || pid != os.Getpid() {
		t.Errorf("peerPID = %d, %v; want %d", pid, err, os.Getpid())
	}
}
