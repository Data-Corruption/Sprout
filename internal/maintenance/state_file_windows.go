//go:build windows

package maintenance

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procReplaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// openStateFile permits atomic state replacement while a process is reading
// state.json. In particular, FILE_SHARE_DELETE is required by MoveFileEx and
// File.Replace; the general no-follow opener intentionally omits it for lock
// files, whose path must remain bound to one file identity while locked.
func openStateFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open maintenance state %s: invalid file handle", path)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened file %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("refuse reparse point %s", path)
	}
	return file, nil
}

func validateStateFile(os.FileInfo) error { return nil }

func replaceFile(oldPath, newPath string) error {
	replaced, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	replacement, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	result, _, callErr := procReplaceFileW.Call(
		uintptr(unsafe.Pointer(replaced)),
		uintptr(unsafe.Pointer(replacement)),
		0,
		0,
		0,
		0,
	)
	if result != 0 {
		return nil
	}
	if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
		return windows.Rename(oldPath, newPath)
	}
	if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
		return callErr
	}
	return windows.ERROR_GEN_FAILURE
}

// Windows has no portable directory fsync operation. WriteState synchronizes
// the replacement file before atomically publishing it.
func syncDirectory(string) error { return nil }
