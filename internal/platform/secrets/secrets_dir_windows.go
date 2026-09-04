//go:build windows

// --- FILE service.https ---

package secrets

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func ensureSecretsDir(path string) error {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("path is a reparse point")
	}
	return nil
}

// checkSecretFile rejects a secret file that is a reparse point. Windows has
// no POSIX mode bits; the directory's inherited ACL governs access.
func checkSecretFile(path string, _ os.FileMode) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s is a reparse point", filepath.Base(path))
	}
	return nil
}
