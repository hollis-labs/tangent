package pluginintent

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLock(f *os.File) error {
	var offset windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}

func unlock(f *os.File) error {
	var offset windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &offset)
}
