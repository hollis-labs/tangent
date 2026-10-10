//go:build unix

package pluginintent

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(f *os.File) error {
	//nolint:gosec // The platform file descriptor fits the platform int.
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return ErrBusy
	}
	return err
}

func unlock(f *os.File) error {
	//nolint:gosec // The platform file descriptor fits the platform int.
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
