package daemon

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/clobrano/ghwatch/internal/config"
)

// Spawn starts `ghwatch -serve` in the background, detached from the
// caller's terminal and session, logging to the state directory. The
// daemon exits AutoIdleExit after its last client disconnects.
func Spawn(paths config.Paths) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	logf, err := os.OpenFile(paths.Log(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "-serve", "-idle-exit", AutoIdleExit.String())
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
