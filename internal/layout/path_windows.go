//go:build windows

package layout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

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
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read process token user: %w", err)
	}
	defaultOwner, err := tokenDefaultOwner(token)
	if err != nil {
		return fmt.Errorf("read process token default owner: %w", err)
	}
	if !ownerMatchesToken(owner, user.User.Sid, defaultOwner) {
		return fmt.Errorf("owner SID is %s, want token user %s or default owner %s", owner, user.User.Sid, defaultOwner)
	}
	return nil
}

func ownerMatchesToken(owner, user, defaultOwner *windows.SID) bool {
	return owner != nil && user != nil && defaultOwner != nil &&
		(owner.Equals(user) || owner.Equals(defaultOwner))
}

func tokenDefaultOwner(token windows.Token) (*windows.SID, error) {
	// Windows assigns TokenOwner to new objects. Under elevation this can be
	// Administrators rather than TokenUser; do not blanket-allow that group for
	// other tokens or repair an existing directory's ownership.
	// https://learn.microsoft.com/windows/win32/secauthz/owner-of-a-new-object
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("token owner query returned no buffer size")
	}
	if size < uint32(unsafe.Sizeof(uintptr(0))) {
		return nil, fmt.Errorf("token owner buffer is too small")
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size); err != nil {
		return nil, err
	}
	// TOKEN_OWNER contains one SID pointer into the returned buffer. Copy the
	// SID so callers never depend on that buffer's lifetime.
	owner := *(**windows.SID)(unsafe.Pointer(&buffer[0]))
	if owner == nil || !owner.IsValid() {
		return nil, fmt.Errorf("token has no valid default owner")
	}
	copy, err := owner.Copy()
	runtime.KeepAlive(buffer)
	return copy, err
}
