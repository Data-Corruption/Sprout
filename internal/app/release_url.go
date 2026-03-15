package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const releaseURLFileName = "release-url"

func releaseURLPath(storageDir string) string {
	return filepath.Join(storageDir, releaseURLFileName)
}

func normalizeReleaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return "", fmt.Errorf("release URL is empty")
	}
	return trimmed + "/", nil
}

func loadReleaseURL(storageDir string) (string, error) {
	if storageDir == "" {
		return "", fmt.Errorf("storage directory is not set")
	}

	path := releaseURLPath(storageDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("release URL is not configured: missing %s", path)
		}
		return "", fmt.Errorf("read release URL file: %w", err)
	}

	url, err := normalizeReleaseURL(string(data))
	if err != nil {
		return "", fmt.Errorf("invalid release URL in %s: %w", path, err)
	}
	return url, nil
}

func (a *App) releaseURL() (string, error) {
	return loadReleaseURL(a.StorageDir)
}
