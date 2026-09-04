//go:build windows

// --- FILE service.https ---

package secrets

import "golang.org/x/sys/windows"

func replaceFile(oldPath, newPath string) error {
	return windows.Rename(oldPath, newPath)
}
