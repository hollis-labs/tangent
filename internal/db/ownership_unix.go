//go:build unix

package db

import (
	"errors"
	"os"
	"syscall"
)

// tryLockExclusive takes a non-blocking exclusive advisory lock on the whole
// file. It reports false — not an error — when another process holds it,
// because "someone else has it" is an answer rather than a fault.
func tryLockExclusive(file *os.File) (bool, error) {
	//nolint:gosec // an *os.File descriptor always fits an int on every platform this builds for
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
		return false, nil
	default:
		return false, err
	}
}

func unlock(file *os.File) error {
	//nolint:gosec // see tryLockExclusive
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
