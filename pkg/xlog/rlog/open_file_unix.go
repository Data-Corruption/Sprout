//go:build linux || darwin || freebsd || netbsd || openbsd

package rlog

import "os"

func openLogFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
