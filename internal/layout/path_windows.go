//go:build windows

package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func storageRoot(appName string) (string, error) {
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", fmt.Errorf("cannot determine LocalApplicationData: %w", err)
	}
	if !filepath.IsAbs(localAppData) {
		return "", fmt.Errorf("LocalApplicationData is not an absolute path: %q", localAppData)
	}
	return filepath.Join(localAppData, windowsDataName(appName)), nil
}

func installerFileName() string { return "install.ps1" }

func windowsDataName(appName string) string {
	if appName == "" {
		return appName
	}
	return strings.ToUpper(appName[:1]) + appName[1:]
}

func ensurePrivateDir(path string) error {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return validatePrivateDir(path)
}

func validatePrivateDir(path string) error {
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
	return validateDirOwner(path)
}

func validateDirOwner(path string) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read directory security descriptor: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read directory owner: %w", err)
	}
	if owner == nil {
		return fmt.Errorf("directory has no owner")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read process token user: %w", err)
	}
	if !owner.Equals(user.User.Sid) {
		return fmt.Errorf("owner SID is %s, want %s", owner, user.User.Sid)
	}
	return nil
}
