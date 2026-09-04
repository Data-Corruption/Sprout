//go:build windows

package xsyscall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// Locking byte 0 rather than the whole range keeps the lock cheap and matches
// what install.ps1 takes on the same files.
const lockRegionBytes = 1

// unlockState carries the OVERLAPPED that LockFileEx was called with;
// UnlockFileEx must be handed the same offset.
type unlockState struct {
	overlapped *windows.Overlapped
}

func tryLock(file *os.File, mode Mode) (unlockState, error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if mode == ModeExclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	state := unlockState{overlapped: new(windows.Overlapped)}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, lockRegionBytes, 0, state.overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return unlockState{}, ErrLocked
	}
	if err != nil {
		return unlockState{}, err
	}
	return state, nil
}

func unlock(file *os.File, state unlockState) error {
	if state.overlapped == nil {
		return nil
	}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, lockRegionBytes, 0, state.overlapped)
}

// OpenNoFollow opens the final component itself, then rejects it if it is a
// reparse point. FILE_FLAG_OPEN_REPARSE_POINT prevents CreateFile from
// resolving the component before it can be inspected.
func OpenNoFollow(path string, flag int, perm fs.FileMode) (*os.File, error) {
	file, err := os.OpenFile(path, flag|windows.O_FILE_FLAG_OPEN_REPARSE_POINT, perm)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened file %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("refuse reparse point %s", path)
	}
	return file, nil
}
