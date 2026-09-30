// Package runlock serializes processes sharing a local data directory.
package runlock

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// Acquire uses an OS lock: a crashed process releases it automatically.
// API: https://pkg.go.dev/golang.org/x/sys/unix#Flock
func Acquire(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".pulse.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("data directory is busy: %w", err)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
