//go:build unix

package daemon

import (
	"errors"
	"os"
	"syscall"
)

// ErrRunning means another daemon holds the lock.
var ErrRunning = errors.New("another ghwatch daemon is already running")

// Lock holds the single-instance lock.
type Lock struct{ f *os.File }

// AcquireLock takes the lock at path without blocking.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release frees the lock.
func (l *Lock) Release() error { return l.f.Close() }

// Running reports whether a daemon holds the lock at path.
func Running(path string) bool {
	l, err := AcquireLock(path)
	if err != nil {
		return errors.Is(err, ErrRunning)
	}
	l.Release()
	return false
}
