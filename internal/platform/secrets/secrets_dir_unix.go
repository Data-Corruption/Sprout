//go:build !windows

// --- FILE service.https ---

package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

// checkSecretFile rejects an existing secret file that is a symlink or carries
// any permission bit outside allowed. Tighter modes pass; looser ones are
// evidence of tampering or a foreign writer and are never repaired.
func checkSecretFile(path string, allowed os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", filepath.Base(path))
	}
	if extra := info.Mode().Perm() &^ allowed; extra != 0 {
		return fmt.Errorf("%s permissions are %04o, want at most %04o",
			filepath.Base(path), info.Mode().Perm(), allowed)
	}
	return nil
}
