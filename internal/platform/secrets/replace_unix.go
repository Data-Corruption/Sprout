//go:build !windows

// --- FILE service.https ---

package secrets

import "os"

func replaceFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
