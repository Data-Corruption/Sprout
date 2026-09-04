//go:build !windows

package maintenance

import (
	"fmt"
	"os"
	"syscall"

	"sprout/pkg/xsyscall"
)

func openStateFile(path string) (*os.File, error) {
	return xsyscall.OpenNoFollow(path, os.O_RDONLY, 0)
}

func validateStateFile(info os.FileInfo) error {
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("maintenance state permissions are %04o, want 0600", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine maintenance state owner")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("maintenance state owner UID is %d, want %d", stat.Uid, os.Getuid())
	}
	return nil
}

func replaceFile(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
