//go:build !windows

package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func storageRoot(appName string) (string, error) {
	if os.Geteuid() == 0 {
		return "", fmt.Errorf("refusing to resolve per-user storage as root")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory is not absolute: %q", home)
	}
	return filepath.Join(home, "."+appName), nil
}

func installerFileName() string { return "install.sh" }

func ensurePrivateDir(path string) error {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		_, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	return validatePrivateDir(path)
}

func validatePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path is a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("permissions are %04o, want 0700", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine directory owner")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("owner UID is %d, want %d", stat.Uid, os.Getuid())
	}
	return nil
}
