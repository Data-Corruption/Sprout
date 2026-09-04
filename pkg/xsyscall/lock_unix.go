//go:build !windows

package xsyscall

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// unlockState is unused on Unix: flock keys off the descriptor alone.
type unlockState struct{}

func tryLock(file *os.File, mode Mode) (unlockState, error) {
	how := unix.LOCK_SH
	if mode == ModeExclusive {
		how = unix.LOCK_EX
	}
	err := unix.Flock(int(file.Fd()), how|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return unlockState{}, ErrLocked
	}
	return unlockState{}, err
}

func unlock(file *os.File, _ unlockState) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

// OpenNoFollow is os.OpenFile with O_NOFOLLOW, so the final path component is
// never resolved through a symlink. It is the open every file under a private
// state directory should use: validatePrivateDir vets the directories, and
// this vets the leaf.
func OpenNoFollow(path string, flag int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|unix.O_NOFOLLOW, perm)
}
